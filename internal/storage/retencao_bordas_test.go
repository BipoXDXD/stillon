package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Bordas exatas da retenção e da consolidação: cada corte é testado na linha
// exatamente no instante de corte e a 1 µs de cada lado (a resolução do
// timestamptz). Só a de 1 µs antes do corte sai; a do corte e a de depois ficam.
// Os testes "bem velho / bem novo" de maintenance_test.go não distinguem `<` de
// `<=` nem `>=` de `>`.

const passoMicro = time.Microsecond

// tresInstantes devolve o corte e os instantes a 1 µs de cada lado, nessa
// ordem: antes, no corte, depois.
func tresInstantes(corte time.Time) [3]time.Time {
	return [3]time.Time{corte.Add(-passoMicro), corte, corte.Add(passoMicro)}
}

// execComoSondador roda um comando no escopo do sondador, que enxerga as
// linhas de todos os donos sob RLS.
func execComoSondador(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
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
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// instantesQueSobraram lê, no escopo do sondador, os instantes de uma coluna
// de tempo, em ordem.
func instantesQueSobraram(t *testing.T, pool *pgxpool.Pool, sql string) []time.Time {
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
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var em []time.Time
	for rows.Next() {
		var x time.Time
		if err := rows.Scan(&x); err != nil {
			t.Fatal(err)
		}
		em = append(em, x.UTC())
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return em
}

// conferirSobra afirma que sobraram exatamente o instante do corte e o de
// depois, e nunca o de antes.
func conferirSobra(t *testing.T, oQue string, sobra []time.Time, i [3]time.Time) {
	t.Helper()
	if len(sobra) != 2 || !sobra[0].Equal(i[1]) || !sobra[1].Equal(i[2]) {
		t.Errorf("%s: sobraram %v; queria só o do corte (%v) e o de 1 µs depois (%v), sem o de 1 µs antes (%v)",
			oQue, sobra, i[1], i[2], i[0])
	}
}

func aplicarRetencao(t *testing.T, pool *pgxpool.Pool, now time.Time) storage.MaintenanceReport {
	t.Helper()
	r, err := storage.ApplyRetention(context.Background(), pool, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// DeleteOldAudit: `at < corte`.
func TestRetentionAuditEdgeIsExclusiveAtTheCutoff(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	i := tresInstantes(agora.AddDate(0, -storage.AuditRetentionMonths, 0))
	for _, em := range i {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, user_id, action, at) VALUES ($1, $2, 'x', $3)`,
			novoID(), dono, em); err != nil {
			t.Fatal(err)
		}
	}
	if r := aplicarRetencao(t, pool, agora); r.DeletedAudit != 1 {
		t.Errorf("apagou %d linhas de auditoria, queria 1", r.DeletedAudit)
	}
	conferirSobra(t, "auditoria", instantesQueSobraram(t, pool, `SELECT at FROM audit_log ORDER BY at`), i)
}

// DeleteExpiredSessions: `expires_at < agora - SessionGrace`.
func TestRetentionSessionsEdgeIsExclusiveAtTheCutoff(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	i := tresInstantes(agora.Add(-storage.SessionGrace))
	for _, em := range i {
		if _, err := pool.Exec(ctx, `INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5)`, novoID(), dono, novoID(), novoID().String(), em); err != nil {
			t.Fatal(err)
		}
	}
	if r := aplicarRetencao(t, pool, agora); r.DeletedSessions != 1 {
		t.Errorf("apagou %d sessões, queria 1", r.DeletedSessions)
	}
	conferirSobra(t, "sessões", instantesQueSobraram(t, pool, `SELECT expires_at FROM sessions ORDER BY expires_at`), i)
}

// DeleteOldHeartbeatEvents: `at < corte`, agora com o lado de depois também.
func TestRetentionHeartbeatEventsEdgeIsExclusiveAtTheCutoff(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hb := novoHeartbeat(t, pool, novoUsuario(t, pool), "a")
	i := tresInstantes(agora.Add(-storage.HeartbeatEventsRetention))
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		for _, em := range i {
			if err := q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
				ID: novoID(), HeartbeatID: hb, At: em, Kind: "success", Source: "ping",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := aplicarRetencao(t, pool, agora); r.DeletedHeartbeatEvents != 1 {
		t.Errorf("apagou %d eventos, queria 1", r.DeletedHeartbeatEvents)
	}
	conferirSobra(t, "eventos de heartbeat", instantesQueSobraram(t, pool, `SELECT at FROM heartbeat_events ORDER BY at`), i)
}

// DeleteOldNotifications: `created_at < corte` (fora de pendente). Cada
// notificação vai num canal próprio, e o incidente nunca avisou a abertura:
// a exceção da linha mais nova por (incidente, canal) não entra no caso.
func TestRetentionNotificationsEdgeIsExclusiveAtTheCutoff(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	inc := novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))
	i := tresInstantes(agora.Add(-storage.NotificationsRetention))
	for _, em := range i {
		id := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), em)
		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			return q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: id, Now: agora})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if r := aplicarRetencao(t, pool, agora); r.DeletedNotifications != 1 {
		t.Errorf("apagou %d notificações, queria 1", r.DeletedNotifications)
	}
	conferirSobra(t, "notificações", instantesQueSobraram(t, pool, `SELECT created_at FROM notifications ORDER BY created_at`), i)
}

// DeleteOldRollups: `bucket < corte`. O bucket só aceita hora cheia (CHECK da
// migration 00006), então a linha fica no corte e o 1 µs de cada lado vem do
// instante da rodada: corte 1 µs antes do bucket, no bucket e 1 µs depois.
func TestRetentionRollupsEdgeIsExclusiveAtTheCutoff(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	bucket := agora.AddDate(0, -storage.RollupRetentionMonths, 0)
	execComoSondador(t, pool, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, 1, 1, 10, 10, 10, 10)`, alvo, bucket)
	contar := func() int { return contarComoSondador(t, pool, `SELECT count(*) FROM check_rollup_1h`) }

	for _, caso := range []struct {
		nome string
		now  time.Time
	}{
		{"corte 1 µs antes do bucket", agora.Add(-passoMicro)},
		{"corte no bucket", agora},
	} {
		if r := aplicarRetencao(t, pool, caso.now); r.DeletedRollups != 0 || contar() != 1 {
			t.Fatalf("%s: apagou %d rollups (restam %d); o bucket não é anterior ao corte e devia ficar",
				caso.nome, r.DeletedRollups, contar())
		}
	}
	if r := aplicarRetencao(t, pool, agora.Add(passoMicro)); r.DeletedRollups != 1 || contar() != 0 {
		t.Errorf("corte 1 µs depois do bucket: apagou %d rollups (restam %d); queria 1 apagado e 0 restantes",
			r.DeletedRollups, contar())
	}
}

// DeleteDeadFamilyProofs, primeiro predicado: `updated_at < agora - folga`.
// Famílias sem sessão nenhuma, portanto mortas.
func TestRetentionDeadFamilyProofsGraceEdgeIsExclusive(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	i := tresInstantes(agora.Add(-storage.FamilyProofsDeadGrace))
	for _, em := range i {
		if _, err := pool.Exec(ctx,
			`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
			 VALUES ($1, $2, $3, 1, 1, $4)`, uuid.New(), dono, uuid.New(), em); err != nil {
			t.Fatal(err)
		}
	}
	if r := aplicarRetencao(t, pool, agora); r.DeletedFamilyProofs != 1 {
		t.Errorf("apagou %d contadores, queria 1", r.DeletedFamilyProofs)
	}
	conferirSobra(t, "contadores de provas",
		instantesQueSobraram(t, pool, `SELECT updated_at FROM session_family_proofs ORDER BY updated_at`), i)
}

// DeleteDeadFamilyProofs, segundo predicado: a família está viva com
// `expires_at > agora`. A sessão que vence exatamente agora já está morta, e o
// contador dela sai; a que vence 1 µs depois ainda está viva.
func TestRetentionDeadFamilyProofsLivenessEdgeIsExclusiveAtNow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	velho := agora.Add(-time.Hour) // bem além da folga
	familiaQueVenceEm := map[uuid.UUID]time.Time{}
	for _, vence := range tresInstantes(agora) {
		familia := uuid.New()
		familiaQueVenceEm[familia] = vence
		if _, err := pool.Exec(ctx, `INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5)`, novoID(), dono, familia, novoID().String(), vence); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
			 VALUES ($1, $2, $3, 1, 1, $4)`, familia, dono, uuid.New(), velho); err != nil {
			t.Fatal(err)
		}
	}
	if r := aplicarRetencao(t, pool, agora); r.DeletedFamilyProofs != 2 {
		t.Errorf("apagou %d contadores, queria 2 (o da sessão vencida 1 µs antes e o da que vence agora)", r.DeletedFamilyProofs)
	}
	rows, err := pool.Query(ctx, `SELECT family_id FROM session_family_proofs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sobra []time.Time
	for rows.Next() {
		var f uuid.UUID
		if err := rows.Scan(&f); err != nil {
			t.Fatal(err)
		}
		sobra = append(sobra, familiaQueVenceEm[f])
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(sobra) != 1 || !sobra[0].Equal(agora.Add(passoMicro)) {
		t.Errorf("sobraram os contadores das famílias que vencem em %v; queria só a que vence 1 µs depois de agora", sobra)
	}
}

// ConsolidateRollups: a janela é [from, to). O check exatamente em from entra;
// o de 1 µs antes não. O de 1 µs antes de to entra; o exatamente em to não.
func TestConsolidationWindowIncludesFromAndExcludesTo(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	de, ate := hora(10, 0), hora(11, 0)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, de); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, de.Add(-passoMicro), "up", 1)   // hora 09, fora
	checkEm(t, pool, alvo, de, "up", 20)                   // hora 10, dentro
	checkEm(t, pool, alvo, ate.Add(-passoMicro), "up", 30) // hora 10, dentro
	checkEm(t, pool, alvo, ate, "up", 4)                   // hora 11, fora

	consolidarJanela(t, pool, de, ate)

	rows := rollupsDe(t, pool, dono, alvo)
	if len(rows) != 1 {
		t.Fatalf("gravou %d rollups (%+v); queria só o da hora 10", len(rows), rows)
	}
	r := rows[0]
	if !r.Bucket.Equal(de) || r.Total != 2 || r.Ok != 2 || valor(r.Max) != 30 || valor(r.P50) != 20 {
		t.Errorf("rollup = bucket %v total %d ok %d p50 %d max %d; queria bucket %v total 2 ok 2 p50 20 max 30",
			r.Bucket, r.Total, r.Ok, valor(r.P50), valor(r.Max), de)
	}
}
