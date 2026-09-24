package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// hora devolve 2026-09-22 às h:m UTC.
func hora(h, m int) time.Time { return time.Date(2026, 9, 22, h, m, 0, 0, time.UTC) }

func checkEm(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID, em time.Time, status string, latencia int32) {
	t.Helper()
	ctx := context.Background()
	p := db.InsertCheckParams{ID: novoID(), TargetID: alvo, CheckedAt: em, Status: status, LatencyMS: latencia}
	if status == "down" {
		k := "timeout"
		p.ErrorKind = &k
	}
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error { return q.InsertCheck(ctx, p) }); err != nil {
		t.Fatalf("gravar check: %v", err)
	}
}

func rollupsDe(t *testing.T, pool *pgxpool.Pool, dono, alvo uuid.UUID) []db.ListRollupsRow {
	t.Helper()
	ctx := context.Background()
	var rows []db.ListRollupsRow
	err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		var err error
		rows, err = q.ListRollups(ctx, db.ListRollupsParams{
			UserID: dono, TargetID: alvo, FromAt: hora(0, 0).AddDate(0, 0, -40), ToAt: hora(0, 0).AddDate(0, 0, 40),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func consolidar(t *testing.T, pool *pgxpool.Pool, em time.Time) {
	t.Helper()
	if _, err := storage.ConsolidateRollups(context.Background(), pool, em); err != nil {
		t.Fatal(err)
	}
}

func valor(p *int32) int32 {
	if p == nil {
		return -1
	}
	return *p
}

// Só horas cheias entram; a hora corrente fica para a próxima rodada. ok
// conta os que não foram down, e as latências são só deles.
func TestConsolidateRollupsAggregatesClosedHours(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "up", 100)
	checkEm(t, pool, alvo, hora(10, 20), "degraded", 300)
	checkEm(t, pool, alvo, hora(10, 40), "down", 5000)
	checkEm(t, pool, alvo, hora(11, 10), "up", 50) // hora corrente

	consolidar(t, pool, hora(11, 30))

	rs := rollupsDe(t, pool, dono, alvo)
	if len(rs) != 1 {
		t.Fatalf("%d rollups, queria 1 (só a hora das 10)", len(rs))
	}
	r := rs[0]
	if !r.Bucket.Equal(hora(10, 0)) || r.Total != 3 || r.Ok != 2 ||
		valor(r.P50) != 100 || valor(r.P95) != 300 || valor(r.Max) != 300 {
		t.Errorf("rollup = %+v p50=%d p95=%d max=%d; queria 10:00, 3 checks, 2 ok, p50 100, p95 e max 300 (o down fora das latências)",
			r, valor(r.P50), valor(r.P95), valor(r.Max))
	}
}

// Rodar de novo reescreve os mesmos números, e um check que chegou atrasado
// na hora já consolidada entra na rodada seguinte.
func TestConsolidationIsIdempotentAndPicksUpLateChecks(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "up", 100)
	consolidar(t, pool, hora(11, 0))
	consolidar(t, pool, hora(11, 0))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 1 || rs[0].Total != 1 {
		t.Fatalf("depois de duas rodadas: %+v", rs)
	}

	checkEm(t, pool, alvo, hora(10, 55), "up", 200) // atrasado
	consolidar(t, pool, hora(11, 15))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 1 || rs[0].Total != 2 || rs[0].Ok != 2 {
		t.Errorf("o check atrasado não entrou: %+v", rs)
	}
}

func TestHourWithoutSuccessHasNoLatency(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "down", 5000)
	consolidar(t, pool, hora(11, 0))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 1 || rs[0].Ok != 0 || rs[0].P50 != nil || rs[0].Max != nil {
		t.Errorf("hora só com falhas: %+v", rs)
	}
}

// Depois de um sondador parado por dias, a rodada seguinte consolida o
// buraco inteiro, e não só as últimas horas.
func TestConsolidationCatchesUpAfterAGap(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(1, 5), "up", 100)
	consolidar(t, pool, hora(2, 0))
	checkEm(t, pool, alvo, hora(15, 5), "up", 100)
	checkEm(t, pool, alvo, hora(20, 5), "up", 100)
	consolidar(t, pool, hora(21, 0))
	if rs := rollupsDe(t, pool, dono, alvo); len(rs) != 3 {
		t.Errorf("%d rollups depois do buraco, queria 3 (01h, 15h e 20h)", len(rs))
	}
}

// Rollups não atravessam donos: a RLS delega ao alvo.
func TestRollupsFollowTheTarget(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, a, agora)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "up", 100)
	consolidar(t, pool, hora(11, 0))
	if n := contarComEscopo(t, pool, a, "check_rollup_1h"); n != 1 {
		t.Errorf("o dono viu %d rollups, queria 1", n)
	}
	if n := contarComEscopo(t, pool, b, "check_rollup_1h"); n != 0 {
		t.Errorf("outro usuário viu %d rollups", n)
	}
}

func existeTabela(t *testing.T, pool *pgxpool.Pool, nome string) bool {
	t.Helper()
	var existe bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1)`, nome).Scan(&existe); err != nil {
		t.Fatal(err)
	}
	return existe
}

// Retenção (spec §12), cada item com o que sai E o que fica: sem o lado do
// que fica, um DELETE sem WHERE passaria.
func TestRetentionRemovesOnlyWhatExpired(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hoje := hora(12, 0) // 2026-09-22
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)

	// Partições de julho a outubro. Corte dos checks: 2026-08-23. Julho
	// termina antes (sai); agosto termina depois (fica).
	for _, mes := range []time.Time{hora(0, 0).AddDate(0, -2, 0), hora(0, 0).AddDate(0, -1, 0), hoje} {
		if err := storage.EnsureChecksPartitions(ctx, pool, mes); err != nil {
			t.Fatal(err)
		}
	}

	// Rollups: um de 14 meses atrás (sai), um de 12 (fica).
	for _, meses := range []int{-14, -12} {
		b := hoje.AddDate(0, meses, 0)
		if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, b); err != nil {
			t.Fatal(err)
		}
		checkEm(t, pool, alvo, b.Add(5*time.Minute), "up", 10)
		consolidarJanela(t, pool, b, b.Add(time.Hour))
	}

	// Notificações: entregue velha (sai), pendente velha (fica), entregue nova (fica).
	inc := novoIncidente(t, pool, alvo)
	velhaEntregue := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), hoje.AddDate(0, 0, -40))
	velhaPendente := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), hoje.AddDate(0, 0, -40))
	novaEntregue := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), hoje.AddDate(0, 0, -10))
	for _, id := range []uuid.UUID{velhaEntregue, novaEntregue} {
		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			return q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: id, Now: hoje})
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Auditoria: 13 meses (sai), 11 (fica). Sessões: vencida há 8 dias
	// (sai), há 6 (fica).
	for _, meses := range []int{-13, -11} {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, user_id, action, at) VALUES ($1, $2, 'x', $3)`,
			novoID(), dono, hoje.AddDate(0, meses, 0)); err != nil {
			t.Fatal(err)
		}
	}
	for _, dias := range []int{-8, -6} {
		if _, err := pool.Exec(ctx, `INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5)`, novoID(), dono, novoID(), novoID().String(), hoje.AddDate(0, 0, dias)); err != nil {
			t.Fatal(err)
		}
	}

	r, err := storage.ApplyRetention(ctx, pool, hoje)
	if err != nil {
		t.Fatal(err)
	}
	if r.DroppedPartitions < 1 || r.DeletedRollups != 1 || r.DeletedNotifications != 1 || r.DeletedAudit != 1 || r.DeletedSessions != 1 {
		t.Errorf("relatório = %+v; queria 1 de cada (e ao menos a partição de julho)", r)
	}
	if existeTabela(t, pool, "checks_2026_07") {
		t.Error("a partição de julho sobreviveu ao corte de 30 dias")
	}
	if !existeTabela(t, pool, "checks_2026_08") {
		t.Error("a partição de agosto, que ainda tem checks dentro dos 30 dias, foi apagada")
	}
	rollups := contarComoSondador(t, pool, `SELECT count(*) FROM check_rollup_1h`)
	sessoes := contarComoSondador(t, pool, `SELECT count(*) FROM sessions`)
	auditoria := contarComoSondador(t, pool, `SELECT count(*) FROM audit_log`)
	notifs := contarComoSondador(t, pool, `SELECT count(*) FROM notifications`)
	pendentes := contarComoSondador(t, pool, `SELECT count(*) FROM notifications WHERE status = 'pending'`)
	if rollups != 1 || sessoes != 1 || auditoria != 1 || notifs != 2 || pendentes != 1 {
		t.Errorf("sobraram rollups=%d sessões=%d auditoria=%d notificações=%d (pendentes %d); queria 1, 1, 1, 2 (1 pendente)",
			rollups, sessoes, auditoria, notifs, pendentes)
	}
	_ = velhaPendente
}

func consolidarJanela(t *testing.T, pool *pgxpool.Pool, de, ate time.Time) {
	t.Helper()
	ctx := context.Background()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		_, err := q.ConsolidateRollups(ctx, db.ConsolidateRollupsParams{FromAt: de, ToAt: ate})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func notificacaoEm(t *testing.T, pool *pgxpool.Pool, incidente, canal uuid.UUID, em time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := novoID()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.EnqueueNotification(ctx, db.EnqueueNotificationParams{
			ID: id, IncidentID: incidente, ChannelID: canal, Event: "opened", Now: em,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// contarComoSondador conta no escopo do sondador, que enxerga as linhas de
// todos os donos (audit_log e sessions nem têm RLS).
func contarComoSondador(t *testing.T, pool *pgxpool.Pool, sql string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRunMaintenanceRejectsANonPositiveInterval(t *testing.T) {
	err := storage.RunMaintenance(context.Background(), nil, clock.NewFake(agora), 0)
	if !errors.Is(err, storage.ErrMaintenanceInterval) {
		t.Errorf("intervalo 0 deu %v, queria ErrMaintenanceInterval", err)
	}
}
