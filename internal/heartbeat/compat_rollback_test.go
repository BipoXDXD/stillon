package heartbeat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/prober"
)

// Piso de rollback: este binário precisa ler linhas gravadas por uma versão
// posterior, que terá agendamentos e faixas que ele não conhece. Os CHECKs de
// hoje impedem gravar essas linhas, então o teste os tira do banco de teste
// (o testdb restaura o snapshot depois de cada teste) e as grava à mão.
func semChecksDeAgendamento(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `ALTER TABLE heartbeats
		DROP CONSTRAINT heartbeats_one_schedule,
		DROP CONSTRAINT heartbeats_period_sec_check,
		DROP CONSTRAINT heartbeats_grace_sec_check,
		DROP CONSTRAINT heartbeats_description_check`); err != nil {
		t.Fatalf("tirar os CHECKs de agendamento: %v", err)
	}
}

// comoDono roda sql no escopo de RLS do dono, que é o único que enxerga a linha.
func (a ambiente) comoDono(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.dono.String()); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("%s: %d linhas afetadas, esperava 1", sql, tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// heartbeatDesconhecido grava a linha de uma versão posterior: sem período e
// sem cron, com o prazo vencido há duas horas e nenhum incidente.
func (a ambiente) heartbeatDesconhecido(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	id, token := a.novoHeartbeat(t)
	a.comoDono(t, `UPDATE heartbeats SET period_sec = NULL, deadline_at = $2 WHERE id = $1`, id, agora.Add(-2*time.Hour))
	return id, token
}

func TestListToleratesRowsFromLaterVersions(t *testing.T) {
	a := novoAmbiente(t)
	semChecksDeAgendamento(t, a.pool)
	normal, _ := a.novoHeartbeat(t)
	foraDaFaixa, _ := a.novoHeartbeat(t)
	a.comoDono(t, `UPDATE heartbeats SET period_sec = 31536000, grace_sec = 31536000, description = $2 WHERE id = $1`,
		foraDaFaixa, strings.Repeat("d", 10000))
	desconhecido, _ := a.novoHeartbeat(t)
	a.comoDono(t, `UPDATE heartbeats SET period_sec = NULL WHERE id = $1`, desconhecido)

	lista, err := heartbeat.NewService(a.pool, a.clk).List(context.Background(), a.dono, nil, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	esperado := []uuid.UUID{desconhecido, foraDaFaixa, normal}
	if len(lista) != len(esperado) {
		t.Fatalf("List devolveu %d heartbeats, esperava %d", len(lista), len(esperado))
	}
	for i, h := range lista {
		if h.ID != esperado[i] {
			t.Errorf("posição %d: id %s, esperava %s", i, h.ID, esperado[i])
		}
	}
	if lista[0].Schedule.IsKnown() {
		t.Error("a linha sem período e sem cron devia ter agendamento desconhecido")
	}
	if got := lista[1].Schedule.Period(); got != 8760*time.Hour {
		t.Errorf("período da linha fora da faixa: %v, esperava 8760h", got)
	}
	if got := lista[1].Grace; got != 8760*time.Hour {
		t.Errorf("tolerância da linha fora da faixa: %v, esperava 8760h", got)
	}
	if got := len(lista[1].Description); got != 10000 {
		t.Errorf("descrição da linha fora da faixa com %d caracteres, esperava 10000", got)
	}
}

// Sem agendamento conhecido não há prazo confiável: o estado nunca vira
// atrasado pelo prazo, mas falha e pausa seguem valendo.
func TestUnknownScheduleStatusIgnoresDeadline(t *testing.T) {
	casos := []struct {
		nome    string
		preparo func(t *testing.T, a ambiente, id uuid.UUID, token string)
		status  heartbeat.Status
	}{
		{"sem execução", func(*testing.T, ambiente, uuid.UUID, string) {}, heartbeat.StatusWaiting},
		{"depois de um sucesso", func(t *testing.T, a ambiente, _ uuid.UUID, token string) {
			a.pingar(t, token, heartbeat.SignalSuccess)
		}, heartbeat.StatusUp},
		{"em falha", func(t *testing.T, a ambiente, _ uuid.UUID, token string) {
			a.pingar(t, token, heartbeat.SignalFail)
		}, heartbeat.StatusFailing},
		{"pausado", func(t *testing.T, a ambiente, id uuid.UUID, _ string) {
			a.comoDono(t, `UPDATE heartbeats SET enabled = false WHERE id = $1`, id)
		}, heartbeat.StatusPaused},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := novoAmbiente(t)
			semChecksDeAgendamento(t, a.pool)
			id, token := a.heartbeatDesconhecido(t)
			c.preparo(t, a, id, token)

			h, err := heartbeat.NewService(a.pool, a.clk).Get(context.Background(), a.dono, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if h.Status != c.status {
				t.Errorf("status %q, esperava %q", h.Status, c.status)
			}
			if !h.DeadlineAt.Equal(agora.Add(-2 * time.Hour)) {
				t.Errorf("prazo %v mudou, esperava %v", h.DeadlineAt, agora.Add(-2*time.Hour))
			}
		})
	}
}

type incidenteRollback struct {
	Causa     string
	Resolvido bool
}

// avaliar roda um tique do avaliador real, o mesmo ponto de entrada do sondador.
func (a ambiente) avaliar(t *testing.T) {
	t.Helper()
	if _, err := prober.NewHeartbeatEvaluator(a.pool, a.clk).Tick(context.Background()); err != nil {
		t.Fatalf("tique do avaliador: %v", err)
	}
}

// incidentes lê no escopo do sondador, sem passar pelo código testado.
func (a ambiente) incidentes(t *testing.T, id uuid.UUID) []incidenteRollback {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT cause, resolved_at IS NOT NULL FROM incidents WHERE heartbeat_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	lista, err := pgx.CollectRows(rows, pgx.RowToStructByPos[incidenteRollback])
	if err != nil {
		t.Fatal(err)
	}
	return lista
}

func TestEvaluatorDoesNotOpenLateForUnknownSchedule(t *testing.T) {
	a := novoAmbiente(t)
	semChecksDeAgendamento(t, a.pool)
	id, _ := a.heartbeatDesconhecido(t)

	a.avaliar(t)

	if got := a.incidentes(t, id); len(got) != 0 {
		t.Errorf("incidentes %+v, esperava nenhum: o prazo de agendamento desconhecido não vale", got)
	}
}

func TestEvaluatorStillOpensFailedForUnknownSchedule(t *testing.T) {
	a := novoAmbiente(t)
	semChecksDeAgendamento(t, a.pool)
	id, token := a.heartbeatDesconhecido(t)
	a.pingar(t, token, heartbeat.SignalFail)

	a.avaliar(t)
	if got := a.incidentes(t, id); len(got) != 1 || got[0] != (incidenteRollback{Causa: "heartbeat_failed"}) {
		t.Fatalf("depois da falha: incidentes %+v, esperava um heartbeat_failed aberto", got)
	}

	a.clk.Advance(time.Minute)
	a.pingar(t, token, heartbeat.SignalSuccess)
	a.avaliar(t)
	if got := a.incidentes(t, id); len(got) != 1 || got[0] != (incidenteRollback{Causa: "heartbeat_failed", Resolvido: true}) {
		t.Errorf("depois do sucesso: incidentes %+v, esperava o heartbeat_failed resolvido", got)
	}
}

// O incidente de atraso foi aberto quando o agendamento ainda era conhecido; a
// linha passa a desconhecida (versão posterior) e o prazo fica no passado.
func TestEvaluatorClosesLateIncidentOfUnknownScheduleAfterSuccess(t *testing.T) {
	a := novoAmbiente(t)
	semChecksDeAgendamento(t, a.pool)
	id, token := a.novoHeartbeat(t)
	a.clk.Advance(2 * time.Hour)
	a.avaliar(t)
	if got := a.incidentes(t, id); len(got) != 1 || got[0] != (incidenteRollback{Causa: "heartbeat_late"}) {
		t.Fatalf("antes: incidentes %+v, esperava um heartbeat_late aberto", got)
	}
	a.comoDono(t, `UPDATE heartbeats SET period_sec = NULL WHERE id = $1`, id)

	a.clk.Advance(time.Minute)
	a.pingar(t, token, heartbeat.SignalSuccess)
	a.avaliar(t)

	if got := a.incidentes(t, id); len(got) != 1 || got[0] != (incidenteRollback{Causa: "heartbeat_late", Resolvido: true}) {
		t.Errorf("depois do sucesso: incidentes %+v, esperava o heartbeat_late resolvido", got)
	}
	if h := a.linha(t, id); !h.DeadlineAt.Equal(agora.Add(time.Hour)) {
		t.Errorf("prazo %v, esperava o antigo %v ainda no passado", h.DeadlineAt, agora.Add(time.Hour))
	}
}
