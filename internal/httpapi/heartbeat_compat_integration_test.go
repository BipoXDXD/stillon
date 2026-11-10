package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Piso de rollback na API: este binário precisa servir linhas gravadas por uma
// versão posterior. Os CHECKs de hoje impedem gravá-las, então o teste os tira
// do banco de teste (o testdb restaura o snapshot depois de cada teste). O
// helper repete o de internal/heartbeat porque os pacotes de teste são outros.
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

// prazoGravado é o prazo que a versão posterior teria deixado na linha.
var prazoGravado = time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)

// comoDonoSQL roda sql no escopo de RLS do dono da API.
func comoDonoSQL(t *testing.T, a *api, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, a.userID.String()); err != nil {
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

// heartbeatDesconhecidoHTTP grava a linha de uma versão posterior: sem período
// e sem cron, com o prazo em prazoGravado.
func heartbeatDesconhecidoHTTP(t *testing.T, a *api) (uuid.UUID, string) {
	t.Helper()
	id, token := novoHeartbeat(t, a)
	comoDonoSQL(t, a, `UPDATE heartbeats SET period_sec = NULL, deadline_at = $2 WHERE id = $1`, id, prazoGravado)
	return id, token
}

func linhaDoHeartbeat(t *testing.T, a *api, id uuid.UUID) db.Heartbeat {
	t.Helper()
	ctx := context.Background()
	var h db.Heartbeat
	err := storage.InUserTx(ctx, a.pool, a.userID, func(q *db.Queries) error {
		var err error
		h, err = q.GetHeartbeatForUpdate(ctx, db.GetHeartbeatForUpdateParams{UserID: a.userID, ID: id})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// valorDe mostra a coluna anulável pelo valor, e não pelo endereço.
func valorDe[T any](p *T) string {
	if p == nil {
		return "NULL"
	}
	return fmt.Sprint(*p)
}

func TestUnknownScheduleIsListedAsUnknown(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, _ := heartbeatDesconhecidoHTTP(t, a)
	dono := tokenDe(t, a, a.userID)

	status, corpo := lerCru(t, a, http.MethodGet, "/api/v1/heartbeats/"+id.String(), dono, nil)
	if status != http.StatusOK {
		t.Fatalf("detalhe: %d %s", status, corpo)
	}
	schedules := []map[string]any{decodificar(t, corpo)["schedule"].(map[string]any)}

	status, corpo = lerCru(t, a, http.MethodGet, "/api/v1/heartbeats", dono, nil)
	if status != http.StatusOK {
		t.Fatalf("lista: %d %s", status, corpo)
	}
	itens := decodificar(t, corpo)["items"].([]any)
	if len(itens) != 1 {
		t.Fatalf("lista com %d itens: %s", len(itens), corpo)
	}
	schedules = append(schedules, itens[0].(map[string]any)["schedule"].(map[string]any))

	for _, s := range schedules {
		if got := chavesDe(s); !slices.Equal(got, []string{"kind"}) || s["kind"] != "unknown" {
			t.Errorf("schedule %v, queria só {\"kind\":\"unknown\"}", s)
		}
	}
}

func TestPingOnUnknownScheduleKeepsDeadline(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, token := heartbeatDesconhecidoHTTP(t, a)

	for _, sufixo := range []string{"", "/start", "/fail", "/0"} {
		a.clk.Advance(time.Minute)
		if status, _, corpo := pingar(t, a, "/ping/"+token+sufixo, nil); status != http.StatusOK {
			t.Fatalf("POST %q: %d %s", sufixo, status, corpo)
		}
		h := linhaDoHeartbeat(t, a, id)
		if !h.DeadlineAt.Equal(prazoGravado) {
			t.Errorf("depois de %q o prazo virou %v, queria %v", sufixo, h.DeadlineAt, prazoGravado)
		}
		if h.LastPingAt == nil || !h.LastPingAt.Equal(a.clk.Now()) {
			t.Errorf("depois de %q last_ping_at = %v, queria %v", sufixo, h.LastPingAt, a.clk.Now())
		}
	}
	if n := contarEventos(t, a, id); n != 4 {
		t.Errorf("%d eventos, queria 4", n)
	}
}

func TestPatchOnUnknownScheduleConflicts(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, _ := heartbeatDesconhecidoHTTP(t, a)
	dono := tokenDe(t, a, a.userID)

	patches := []map[string]any{
		{"name": "nome novo"},
		{"enabled": false},
		{"schedule": map[string]any{"kind": "period", "period_sec": 3600}},
	}
	for _, patch := range patches {
		a.clk.Advance(time.Minute)
		status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id.String(), dono, patch)
		if status != http.StatusConflict || !strings.Contains(corpo, `"code":"schedule_unsupported"`) {
			t.Errorf("PATCH %v: %d %s, queria 409 schedule_unsupported", patch, status, corpo)
		}
		h := linhaDoHeartbeat(t, a, id)
		if h.Name != "backup noturno" || !h.Enabled || !h.DeadlineAt.Equal(prazoGravado) ||
			h.PeriodSec != nil || h.CronExpr != nil {
			t.Errorf("PATCH %v gravou: nome %q, enabled %v, prazo %v, período %s, cron %s",
				patch, h.Name, h.Enabled, h.DeadlineAt, valorDe(h.PeriodSec), valorDe(h.CronExpr))
		}
	}
}

// O 409 schedule_unsupported não pode vazar a existência da linha alheia: para
// quem não é dono, a linha desconhecida é 404 como qualquer outra.
func TestPatchByOtherUserOnUnknownScheduleIsNotFound(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, _ := heartbeatDesconhecidoHTTP(t, a)
	intruso := tokenDe(t, a, outroUsuario(t, a))

	a.clk.Advance(time.Minute)
	status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id.String(), intruso, map[string]any{"name": "x"})
	if status != http.StatusNotFound || !strings.Contains(corpo, `"code":"not_found"`) ||
		strings.Contains(corpo, "schedule_unsupported") {
		t.Errorf("PATCH alheio: %d %s, queria 404 not_found sem schedule_unsupported", status, corpo)
	}
	h := linhaDoHeartbeat(t, a, id)
	if h.Name != "backup noturno" || !h.Enabled || !h.DeadlineAt.Equal(prazoGravado) ||
		h.PeriodSec != nil || h.CronExpr != nil {
		t.Errorf("PATCH alheio gravou: nome %q, enabled %v, prazo %v, período %s, cron %s",
			h.Name, h.Enabled, h.DeadlineAt, valorDe(h.PeriodSec), valorDe(h.CronExpr))
	}
}

func TestDeleteAndRotateStillWorkOnUnknownSchedule(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	apagar, _ := heartbeatDesconhecidoHTTP(t, a)
	rotacionar, _ := heartbeatDesconhecidoHTTP(t, a)
	dono := tokenDe(t, a, a.userID)

	if status, corpo := lerCru(t, a, http.MethodDelete, "/api/v1/heartbeats/"+apagar.String(), dono, nil); status != http.StatusNoContent {
		t.Errorf("DELETE: %d %s", status, corpo)
	}
	status, corpo := lerCru(t, a, http.MethodPost, "/api/v1/heartbeats/"+rotacionar.String()+"/token/rotate", dono, nil)
	if status != http.StatusOK {
		t.Fatalf("rotação: %d %s", status, corpo)
	}
	if s := decodificar(t, corpo)["schedule"].(map[string]any); s["kind"] != "unknown" {
		t.Errorf("rotação devolveu schedule %v", s)
	}
}

func TestLongPeriodRowPingsAndPatchOfOtherFieldsWorks(t *testing.T) {
	a := newAPI(t)
	semChecksDeAgendamento(t, a.pool)
	id, token := novoHeartbeat(t, a)
	comoDonoSQL(t, a, `UPDATE heartbeats SET period_sec = 31536000 WHERE id = $1`, id)
	dono := tokenDe(t, a, a.userID)

	a.clk.Advance(time.Minute)
	if status, _, corpo := pingar(t, a, "/ping/"+token, nil); status != http.StatusOK {
		t.Fatalf("ping: %d %s", status, corpo)
	}
	// 365 dias de período mais os 300 s de tolerância do novoHeartbeat.
	if got, want := linhaDoHeartbeat(t, a, id).DeadlineAt, a.clk.Now().Add(365*24*time.Hour+300*time.Second); !got.Equal(want) {
		t.Errorf("prazo depois do ping %v, queria %v", got, want)
	}

	status, corpo := lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id.String(), dono, map[string]any{"name": "anual"})
	if status != http.StatusOK {
		t.Fatalf("PATCH do nome: %d %s", status, corpo)
	}
	if h := linhaDoHeartbeat(t, a, id); h.Name != "anual" || h.PeriodSec == nil || *h.PeriodSec != 31536000 {
		t.Errorf("PATCH do nome: nome %q, período %s", h.Name, valorDe(h.PeriodSec))
	}

	antes := linhaDoHeartbeat(t, a, id)
	status, corpo = lerCru(t, a, http.MethodPatch, "/api/v1/heartbeats/"+id.String(), dono, map[string]any{
		"name": "outro", "schedule": map[string]any{"kind": "period", "period_sec": 31536001},
	})
	if status != http.StatusBadRequest || !strings.Contains(corpo, `"reason":"out_of_range"`) {
		t.Errorf("PATCH com período acima do teto de cadastro: %d %s, queria 400 out_of_range", status, corpo)
	}
	depois := linhaDoHeartbeat(t, a, id)
	if depois.Name != "anual" || depois.PeriodSec == nil || *depois.PeriodSec != 31536000 || !depois.DeadlineAt.Equal(antes.DeadlineAt) {
		t.Errorf("PATCH recusado gravou: nome %q, período %s, prazo %v", depois.Name, valorDe(depois.PeriodSec), depois.DeadlineAt)
	}
}
