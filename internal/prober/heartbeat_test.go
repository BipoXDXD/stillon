package prober_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

var inicioHB = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// Um heartbeat de hora em hora nasce vencendo uma hora depois do início.
const prazoHB = time.Hour

type bancadaHB struct {
	pool *pgxpool.Pool
	clk  *clock.Fake
	av   *prober.HeartbeatEvaluator
	pg   *heartbeat.PingService
	dono uuid.UUID
}

func novaBancadaHB(t *testing.T) *bancadaHB {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(inicioHB)
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(t.Context(), `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		dono, dono.String()+"@exemplo.test"); err != nil {
		t.Fatal(err)
	}
	return &bancadaHB{pool: pool, clk: clk, av: prober.NewHeartbeatEvaluator(pool, clk), pg: heartbeat.NewPingService(pool, clk), dono: dono}
}

func (b *bancadaHB) heartbeat(t *testing.T, nome string) (uuid.UUID, string) {
	t.Helper()
	ctx := t.Context()
	id := uuid.Must(uuid.NewV7())
	token, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: b.dono, Name: nome, PingTokenHash: hash, PeriodSec: &periodo, GraceSec: 300,
			Enabled: true, CreatedAt: inicioHB, DeadlineAt: inicioHB.Add(prazoHB),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, token
}

func (b *bancadaHB) canal(t *testing.T) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	id := uuid.Must(uuid.NewV7())
	agora := b.clk.Now()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertChannel(ctx, db.InsertChannelParams{
			ID: id, UserID: b.dono, Kind: "discord", ConfigEnc: []byte{1}, VerifiedAt: &agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *bancadaHB) tick(t *testing.T) int {
	t.Helper()
	n, err := b.av.Tick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (b *bancadaHB) pingar(t *testing.T, token string, sig heartbeat.Signal) {
	t.Helper()
	if err := b.pg.Ping(t.Context(), token, sig); err != nil {
		t.Fatal(err)
	}
}

func (b *bancadaHB) pausar(t *testing.T, id uuid.UUID, ligado bool) {
	t.Helper()
	if _, err := heartbeat.NewService(b.pool, b.clk).Update(t.Context(), b.dono, id, heartbeat.Patch{Enabled: &ligado}); err != nil {
		t.Fatal(err)
	}
}

type incidenteHB struct {
	ID     uuid.UUID
	Causa  string
	Inicio time.Time
	Fim    *time.Time
	Avisos []string
}

// incidentes lê tudo como o sondador, sem passar pelo que está sendo testado.
func (b *bancadaHB) incidentes(t *testing.T, id uuid.UUID) []incidenteHB {
	t.Helper()
	ctx := context.Background()
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT id, cause, started_at, resolved_at FROM incidents WHERE heartbeat_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	lista, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (incidenteHB, error) {
		var i incidenteHB
		return i, r.Scan(&i.ID, &i.Causa, &i.Inicio, &i.Fim)
	})
	if err != nil {
		t.Fatal(err)
	}
	for k := range lista {
		rows, err := tx.Query(ctx, `SELECT event || ':' || coalesce(target_name, '') FROM notifications WHERE incident_id = $1 ORDER BY id`, lista[k].ID)
		if err != nil {
			t.Fatal(err)
		}
		lista[k].Avisos, err = pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
	}
	return lista
}

func TestLateHeartbeatOpensIncidentAndNotifies(t *testing.T) {
	b := novaBancadaHB(t)
	id, _ := b.heartbeat(t, "backup")
	b.canal(t)

	b.clk.Advance(prazoHB - time.Nanosecond)
	if n := b.tick(t); n != 0 {
		t.Fatalf("antes do prazo: %d transições", n)
	}
	b.clk.Advance(time.Nanosecond)
	if n := b.tick(t); n != 1 {
		t.Fatalf("no prazo: %d transições, queria 1", n)
	}
	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Causa != heartbeat.CauseLate || !l[0].Inicio.Equal(inicioHB.Add(prazoHB)) || l[0].Fim != nil {
		t.Fatalf("incidentes: %+v", l)
	}
	if len(l[0].Avisos) != 1 || l[0].Avisos[0] != "opened:backup" {
		t.Errorf("avisos: %v", l[0].Avisos)
	}
	if n := b.tick(t); n != 0 {
		t.Errorf("segundo tique: %d transições", n)
	}
}

func TestPingOnTimeOpensNothing(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.clk.Advance(prazoHB - time.Minute)
	b.pingar(t, token, heartbeat.SignalSuccess)
	b.clk.Advance(time.Hour)
	if n := b.tick(t); n != 0 {
		t.Errorf("%d transições com o ping em dia", n)
	}
	if l := b.incidentes(t, id); len(l) != 0 {
		t.Errorf("incidentes: %+v", l)
	}
}

// Só vira atraso depois de período + tolerância: a tolerância é a do heartbeat.
func TestDeadlineAfterAPingIncludesTheGrace(t *testing.T) {
	b := novaBancadaHB(t)
	_, token := b.heartbeat(t, "backup")
	b.pingar(t, token, heartbeat.SignalSuccess)
	b.clk.Advance(time.Hour + 5*time.Minute - time.Second)
	if n := b.tick(t); n != 0 {
		t.Fatalf("dentro da tolerância: %d transições", n)
	}
	b.clk.Advance(time.Second)
	if n := b.tick(t); n != 1 {
		t.Fatalf("fim da tolerância: %d transições", n)
	}
}

func TestFailSignalOpensIncidentWithItsOwnCause(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.canal(t)
	b.clk.Advance(time.Minute)
	b.pingar(t, token, heartbeat.SignalFail)
	if n := b.tick(t); n != 1 {
		t.Fatalf("%d transições, queria 1", n)
	}
	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Causa != heartbeat.CauseFailed || !l[0].Inicio.Equal(inicioHB.Add(time.Minute)) {
		t.Fatalf("incidentes: %+v", l)
	}
}

func TestSuccessClosesTheIncidentAndNotifies(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.canal(t)
	b.clk.Advance(2 * time.Hour)
	b.tick(t)

	b.clk.Advance(10 * time.Minute)
	sucesso := b.clk.Now()
	b.pingar(t, token, heartbeat.SignalSuccess)
	b.clk.Advance(time.Second)
	if n := b.tick(t); n != 1 {
		t.Fatalf("%d transições, queria o fechamento", n)
	}
	l := b.incidentes(t, id)
	if len(l) != 1 || l[0].Fim == nil || !l[0].Fim.Equal(sucesso) {
		t.Fatalf("incidentes: %+v; queria fim em %v", l, sucesso)
	}
	if len(l[0].Avisos) != 2 || l[0].Avisos[1] != "resolved:backup" {
		t.Errorf("avisos: %v", l[0].Avisos)
	}
}

// Um início não fecha: quem diz que deu certo é o sucesso.
func TestStartDoesNotCloseTheIncident(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.clk.Advance(2 * time.Hour)
	b.tick(t)
	b.pingar(t, token, heartbeat.SignalStart)
	b.clk.Advance(time.Minute)
	if n := b.tick(t); n != 0 {
		t.Fatalf("%d transições depois do início", n)
	}
	if l := b.incidentes(t, id); l[0].Fim != nil {
		t.Errorf("o início fechou o incidente: %+v", l[0])
	}
}

// A falha avisada segura o incidente aberto com o prazo ainda em dia: só o
// sucesso fecha.
func TestFailedHeartbeatStaysOpenUntilSuccess(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.pingar(t, token, heartbeat.SignalFail)
	b.tick(t)
	b.clk.Advance(time.Minute)
	if n := b.tick(t); n != 0 {
		t.Fatalf("%d transições com a falha pendente", n)
	}
	if l := b.incidentes(t, id); l[0].Fim != nil {
		t.Fatalf("fechou sem sucesso: %+v", l[0])
	}
}

func TestPausedHeartbeatIsNotEvaluated(t *testing.T) {
	b := novaBancadaHB(t)
	id, _ := b.heartbeat(t, "backup")
	b.pausar(t, id, false)
	b.clk.Advance(2 * time.Hour)
	if n := b.tick(t); n != 0 {
		t.Errorf("heartbeat pausado teve %d transições", n)
	}
}

// Com incidente aberto, pausar não fecha: como o alvo pausado, fica aberto.
func TestPausingKeepsTheOpenIncident(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.clk.Advance(2 * time.Hour)
	b.tick(t)
	b.pausar(t, id, false)
	b.pingar(t, token, heartbeat.SignalSuccess)
	b.clk.Advance(time.Minute)
	if n := b.tick(t); n != 0 {
		t.Errorf("%d transições com o heartbeat pausado", n)
	}
}

// D11: token vazado alternando falha e sucesso gera no máximo uma abertura a
// cada 5 min. A falha que ficou esperando abre depois: atrasa, não se perde.
func TestReopenCooldownLimitsIncidents(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.canal(t)

	b.pingar(t, token, heartbeat.SignalFail)
	b.tick(t)
	b.clk.Advance(30 * time.Second)
	b.pingar(t, token, heartbeat.SignalSuccess)
	b.tick(t)
	if l := b.incidentes(t, id); len(l) != 1 || l[0].Fim == nil {
		t.Fatalf("primeiro ciclo: %+v", l)
	}

	b.clk.Advance(30 * time.Second)
	b.pingar(t, token, heartbeat.SignalFail)
	b.tick(t)
	if l := b.incidentes(t, id); len(l) != 1 {
		t.Fatalf("reabriu dentro do intervalo: %+v", l)
	}

	b.clk.Advance(heartbeat.ReopenCooldown - time.Minute - time.Nanosecond)
	b.tick(t)
	if l := b.incidentes(t, id); len(l) != 1 {
		t.Fatalf("reabriu antes do fim do intervalo: %+v", l)
	}
	b.clk.Advance(time.Nanosecond)
	b.tick(t)
	if l := b.incidentes(t, id); len(l) != 2 || l[1].Causa != heartbeat.CauseFailed {
		t.Fatalf("a falha que esperou não abriu: %+v", l)
	}
}

// Duas instâncias no mesmo instante: um incidente e um aviso por canal.
func TestTwoEvaluatorsOpenOneIncident(t *testing.T) {
	b := novaBancadaHB(t)
	id, _ := b.heartbeat(t, "backup")
	b.canal(t)
	b.clk.Advance(2 * time.Hour)
	outro := prober.NewHeartbeatEvaluator(b.pool, b.clk)

	var wg sync.WaitGroup
	erros := make(chan error, 8)
	largada := make(chan struct{})
	for i := range 8 {
		av := b.av
		if i%2 == 1 {
			av = outro
		}
		wg.Go(func() {
			<-largada
			_, err := av.Tick(context.Background())
			erros <- err
		})
	}
	close(largada)
	wg.Wait()
	close(erros)
	for err := range erros {
		if err != nil {
			t.Error(err)
		}
	}
	l := b.incidentes(t, id)
	if len(l) != 1 || len(l[0].Avisos) != 1 {
		t.Fatalf("incidentes: %+v", l)
	}
}

// Sem memória: quem fecha é uma instância que nunca viu o incidente abrir.
func TestEvaluatorKeepsNoStateBetweenInstances(t *testing.T) {
	b := novaBancadaHB(t)
	id, token := b.heartbeat(t, "backup")
	b.clk.Advance(2 * time.Hour)
	b.tick(t)
	b.pingar(t, token, heartbeat.SignalSuccess)
	novo := prober.NewHeartbeatEvaluator(b.pool, b.clk)
	if n, err := novo.Tick(t.Context()); err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v", n, err)
	}
	if l := b.incidentes(t, id); l[0].Fim == nil {
		t.Errorf("o incidente não fechou: %+v", l[0])
	}
}

// SKIP LOCKED: um ping travando a linha não segura o tique, e o vizinho passa.
func TestEvaluatorSkipsAHeartbeatThatIsLocked(t *testing.T) {
	b := novaBancadaHB(t)
	ctx := t.Context()
	travado, _ := b.heartbeat(t, "travado")
	livre, _ := b.heartbeat(t, "livre")
	b.clk.Advance(2 * time.Hour)

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM heartbeats WHERE id = $1 FOR UPDATE`, travado); err != nil {
		t.Fatal(err)
	}

	curto, cancelar := context.WithTimeout(ctx, 3*time.Second)
	defer cancelar()
	n, err := b.av.Tick(curto)
	if err != nil || n != 1 {
		t.Fatalf("Tick = %d, %v; queria 1, sem esperar a trava", n, err)
	}
	if l := b.incidentes(t, livre); len(l) != 1 {
		t.Errorf("o vizinho ficou sem incidente: %+v", l)
	}
}

func TestEvaluatorPagesThroughEveryHeartbeat(t *testing.T) {
	b := novaBancadaHB(t)
	var ids []uuid.UUID
	for range 3 {
		id, _ := b.heartbeat(t, "h")
		ids = append(ids, id)
	}
	av := prober.NewHeartbeatEvaluatorComPagina(b.pool, b.clk, 1)
	b.clk.Advance(2 * time.Hour)
	if n, err := av.Tick(t.Context()); err != nil || n != 3 {
		t.Fatalf("Tick = %d, %v; queria 3", n, err)
	}
	for _, id := range ids {
		if l := b.incidentes(t, id); len(l) != 1 {
			t.Errorf("%s: %+v", id, l)
		}
	}
}

// O dispatcher e os lembretes tratam o incidente de heartbeat como os outros:
// mesmo nome, mesmo fan-out, e pausar corta os lembretes.
func TestHeartbeatIncidentFlowsThroughTheOutbox(t *testing.T) {
	b := novaBancadaHB(t)
	ctx := t.Context()
	id, _ := b.heartbeat(t, "backup noturno")
	b.canal(t)
	b.clk.Advance(2 * time.Hour)
	b.tick(t)

	var entregaveis []db.ClaimDueNotificationsRow
	err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
		var err error
		entregaveis, err = q.ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
			Now: b.clk.Now(), LeaseUntil: b.clk.Now().Add(time.Minute), MaxRows: 10,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entregaveis) != 1 {
		t.Fatalf("o dispatcher recebeu %d linhas, queria 1", len(entregaveis))
	}
	n := entregaveis[0]
	if n.TargetName != "backup noturno" || !n.TargetEnabled || n.Cause != heartbeat.CauseLate {
		t.Errorf("linha do dispatcher: nome %q, ativo %v, causa %q", n.TargetName, n.TargetEnabled, n.Cause)
	}
	lembretes := notify.NewReminders(b.pool, b.clk, notify.DefaultPolicy())
	b.clk.Advance(31 * time.Minute)
	if q, err := lembretes.Tick(ctx); err != nil || q != 1 {
		t.Fatalf("lembrete: %d, %v", q, err)
	}
	b.pausar(t, id, false)
	b.clk.Advance(31 * time.Minute)
	if q, err := lembretes.Tick(ctx); err != nil || q != 0 {
		t.Fatalf("lembrete com o heartbeat pausado: %d, %v", q, err)
	}
	avisos := strings.Join(b.incidentes(t, id)[0].Avisos, ",")
	if avisos != "opened:backup noturno,reminder:backup noturno" {
		t.Errorf("avisos: %s", avisos)
	}
}
