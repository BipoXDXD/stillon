package storage_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// semearVencidos grava n linhas vencidas em cada tabela da retenção por
// DELETE (rollups, notificações entregues, eventos de heartbeat, auditoria e
// sessões), todas do mesmo dono.
func semearVencidos(t *testing.T, pool *pgxpool.Pool, hoje time.Time, n int) {
	t.Helper()
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	velho := hoje.AddDate(0, -14, 0)
	if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, velho); err != nil {
		t.Fatal(err)
	}
	inc := novoIncidente(t, pool, alvo)
	hb := novoHeartbeat(t, pool, dono, "lotes")
	for i := range n {
		b := velho.Add(time.Duration(i) * time.Hour)
		checkEm(t, pool, alvo, b.Add(5*time.Minute), "up", 10)
		consolidarJanela(t, pool, b, b.Add(time.Hour))

		id := notificacaoEm(t, pool, inc, novoCanal(t, pool, dono, nil, time.Time{}), hoje.AddDate(0, 0, -40))
		if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			if err := q.MarkNotificationSent(ctx, db.MarkNotificationSentParams{ID: id, Now: hoje}); err != nil {
				return err
			}
			return q.InsertHeartbeatEvent(ctx, db.InsertHeartbeatEventParams{
				ID: novoID(), HeartbeatID: hb, At: hoje.AddDate(0, 0, -40), Kind: "success", Source: "ping",
			})
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO audit_log (id, user_id, action, at) VALUES ($1, $2, 'x', $3)`,
			novoID(), dono, hoje.AddDate(0, -13, 0)); err != nil {
			t.Fatal(err)
		}
		if _, err := testdb.Admin(t).Exec(ctx, `INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at)
			VALUES ($1, $2, $3, $4, $5)`, novoID(), dono, novoID(), uuid.NewString(), hoje.AddDate(0, 0, -8)); err != nil {
			t.Fatal(err)
		}
	}
}

func apagadosPorTabela(r storage.MaintenanceReport) [5]int64 {
	return [5]int64{r.DeletedRollups, r.DeletedNotifications, r.DeletedHeartbeatEvents, r.DeletedAudit, r.DeletedSessions}
}

// A retenção apaga em lotes, cada um na sua transação, e para no teto de lotes
// da rodada: um DELETE único de meses de acúmulo passava do statement_timeout
// e voltava inteiro, rodada após rodada. Com lote 1 e teto 2, três vencidas
// por tabela saem duas numa rodada e a última na seguinte.
func TestRetentionDeletesInBatchesUpToTheCapPerRound(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hoje := hora(12, 0)
	semearVencidos(t, pool, hoje, 3)

	r, err := storage.ApplyRetentionInBatches(ctx, pool, hoje, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := apagadosPorTabela(r); got != [5]int64{2, 2, 2, 2, 2} {
		t.Errorf("primeira rodada apagou %v (rollups, notificações, eventos, auditoria, sessões); queria 2 de cada", got)
	}
	r, err = storage.ApplyRetentionInBatches(ctx, pool, hoje, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := apagadosPorTabela(r); got != [5]int64{1, 1, 1, 1, 1} {
		t.Errorf("segunda rodada apagou %v; queria a última de cada", got)
	}
}

// Uma tabela que falha não segura as outras, nem o DROP das partições, nem a
// limpeza dos contadores de provas: o erro dela sobe com o nome da tabela, e o
// resto da retenção vale.
func TestRetentionOtherTablesSurviveAFailingTable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hoje := hora(12, 0)
	for _, mes := range []time.Time{hoje.AddDate(0, -2, 0), hoje} {
		if err := storage.EnsureChecksPartitions(ctx, pool, mes); err != nil {
			t.Fatal(err)
		}
	}
	semearVencidos(t, pool, hoje, 1)
	// Um contador de provas de família morta (sem sessão), velho além da folga.
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO session_family_proofs (family_id, user_id, generation, reserved, errors, updated_at)
		 VALUES ($1, $2, $3, 1, 1, $4)`, uuid.New(), novoUsuario(t, pool), uuid.New(), hoje.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(ctx, `DROP TABLE audit_log CASCADE`); err != nil {
		t.Fatal(err)
	}

	r, err := storage.ApplyRetention(ctx, pool, hoje)
	if err == nil || !strings.Contains(err.Error(), "auditoria") {
		t.Fatalf("com audit_log fora, a retenção deu %v; queria o erro da auditoria", err)
	}
	if r.DeletedRollups != 1 || r.DeletedNotifications != 1 || r.DeletedHeartbeatEvents != 1 || r.DeletedSessions != 1 {
		t.Errorf("relatório %+v; queria 1 de cada tabela que não falhou", r)
	}
	if r.DroppedPartitions < 1 || r.DeletedFamilyProofs != 1 {
		t.Errorf("o DROP das partições vencidas ou a limpeza dos contadores de provas não rodou: %+v", r)
	}
}

// Um lote que falha depois de outros comitados não apaga a contagem deles: o
// relatório diz o que de fato saiu do banco, e o laço para no erro.
func TestRetentionBatchFailureKeepsCommittedCount(t *testing.T) {
	pool := testdb.New(t)
	falha := errors.New("lote quebrado")
	chamadas := 0
	total, err := storage.DeleteInBatches(context.Background(), pool, 3, 5, func(*db.Queries, int32) (int64, error) {
		chamadas++
		if chamadas == 3 {
			return 0, falha
		}
		return 3, nil
	})
	if !errors.Is(err, falha) {
		t.Fatalf("erro %v; queria o do lote", err)
	}
	if total != 6 || chamadas != 3 {
		t.Errorf("total %d em %d chamadas; queria 6 (dois lotes de 3 comitados) em 3", total, chamadas)
	}
}
