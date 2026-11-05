package storage_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Merge é deploy, e rollback é reimplantar a imagem anterior com o schema
// novo: o schema não desce (spec de heartbeats, §3). Estes textos são o SQL da
// main de antes da 00016, nas queries que a mudam: joins e filtros exatos, com
// a projeção reduzida ao que importa nas duas filas. Não atualize junto com as
// queries: a imagem anterior não muda.
const (
	antigaListIncidents = `SELECT i.id, i.target_id, t.name AS target_name, i.started_at, i.resolved_at, i.cause, i.ack_at
  FROM incidents i
  JOIN targets t ON t.id = i.target_id
 WHERE t.user_id = $1
   AND ($2::bool IS NULL OR (i.resolved_at IS NULL) = $2::bool)
   AND ($3::uuid IS NULL OR i.id < $3::uuid)
   AND ($4::uuid IS NULL OR i.target_id = $4::uuid)
 ORDER BY i.id DESC
 LIMIT $5::int`

	antigaAckIncident = `UPDATE incidents i
   SET ack_at = COALESCE(i.ack_at, $1::timestamptz)
  FROM targets t
 WHERE t.id = i.target_id AND t.user_id = $2 AND i.id = $3
RETURNING i.id, i.target_id, t.name AS target_name, i.started_at, i.resolved_at, i.cause, i.ack_at`

	antigaGetOpenIncident = `SELECT id, target_id, started_at, resolved_at, cause, ack_at, open_notified_at, close_notified_at
  FROM incidents WHERE target_id = $1 AND resolved_at IS NULL`

	antigaOpenIncident = `INSERT INTO incidents (id, target_id, started_at, cause) VALUES ($1, $2, $3, $4)`

	antigaClaimDueNotifications = `WITH claimed AS (
    UPDATE notifications n
       SET next_attempt_at = $1::timestamptz, attempts = n.attempts + 1
     WHERE n.id IN (
           SELECT id FROM notifications
            WHERE status = 'pending' AND next_attempt_at <= $2::timestamptz
            ORDER BY next_attempt_at
            LIMIT $3::int
              FOR UPDATE SKIP LOCKED)
    RETURNING n.id, n.incident_id, n.channel_id, n.event, n.attempts, n.created_at, n.target_name
)
SELECT cl.id, i.target_id
  FROM claimed cl
  JOIN notif_channels c ON c.id = cl.channel_id
  JOIN incidents i ON i.id = cl.incident_id
  JOIN targets t ON t.id = i.target_id`

	antigaListReminderCandidates = `SELECT n.incident_id, n.channel_id
  FROM notifications n
  JOIN incidents i ON i.id = n.incident_id
  JOIN targets t ON t.id = i.target_id
  JOIN notif_channels c ON c.id = n.channel_id
 WHERE n.event IN ('opened', 'reminder')
   AND i.resolved_at IS NULL AND i.ack_at IS NULL AND i.open_notified_at IS NOT NULL
   AND t.enabled
   AND c.enabled AND c.verified_at IS NOT NULL
 GROUP BY n.incident_id, n.channel_id`
)

// TestOlderBinaryIgnoresHeartbeatRows prova o que a spec promete para um
// rollback com heartbeats gravados: o binário anterior continua sondando,
// listando e avisando o que é de alvo, e não enxerga o resto.
func TestOlderBinaryIgnoresHeartbeatRows(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	canal := novoCanal(t, pool, dono, nil, time.Time{})

	alvo := novoAlvo(t, pool, dono, agora.Add(-time.Hour))
	incidenteAlvo := novoIncidente(t, pool, alvo)
	notifAlvo := novaNotificacao(t, pool, incidenteAlvo, canal)

	hb := novoHeartbeat(t, pool, dono, "backup")
	incidenteHB := novoIncidenteDeHeartbeat(t, pool, hb)
	notifHB := novaNotificacao(t, pool, incidenteHB, canal)
	marcarAberturaAvisada(t, pool, incidenteAlvo, incidenteHB)

	// As queries que não mudaram com a 00016 são as do próprio pacote db.
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		alvos, err := q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
		if err != nil {
			return err
		}
		if len(alvos) != 1 || alvos[0].ID != alvo {
			t.Errorf("reivindicou %+v, queria só o alvo", alvos)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ClaimDueTargets: %v", err)
	}
	err = storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
		l, err := q.ListTargets(ctx, db.ListTargetsParams{UserID: dono, MaxRows: 10})
		if err != nil {
			return err
		}
		s, err := q.ListTargetSummaries(ctx, dono)
		if err != nil {
			return err
		}
		if len(l) != 1 || len(s) != 1 {
			t.Errorf("%d alvos e %d resumos, queria 1 de cada", len(l), len(s))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("leituras de alvos: %v", err)
	}

	// target_id num uuid.UUID, e não num ponteiro: é o que o binário anterior
	// escaneia, e um NULL aqui derrubaria a rota.
	err = emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		var ids []uuid.UUID
		rows, err := tx.Query(ctx, antigaListIncidents, dono, nil, nil, nil, 10)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, alvoID uuid.UUID
			var nome, causa string
			var inicio time.Time
			var fim, ack *time.Time
			if err := rows.Scan(&id, &alvoID, &nome, &inicio, &fim, &causa, &ack); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if len(ids) != 1 || ids[0] != incidenteAlvo {
			t.Errorf("ListIncidents antiga devolveu %v, queria só o do alvo", ids)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("ListIncidents antiga: %v", err)
	}

	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		var id, alvoID uuid.UUID
		var a, b, c, d *time.Time
		var causa string
		var inicio time.Time
		if err := tx.QueryRow(ctx, antigaGetOpenIncident, alvo).Scan(&id, &alvoID, &inicio, &a, &causa, &b, &c, &d); err != nil {
			return err
		}
		if id != incidenteAlvo {
			t.Errorf("GetOpenIncident antiga devolveu %s", id)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("GetOpenIncident antiga: %v", err)
	}
	abrirNoAlvoNovo(t, pool, dono)

	// A notificação de heartbeat é reivindicada e descartada pelo join: não
	// quebra, não é entregue, e gasta tentativas (custo aceito na §3).
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, antigaClaimDueNotifications, agora.Add(time.Hour), agora.Add(time.Hour), 10)
		if err != nil {
			return err
		}
		defer rows.Close()
		var ids []uuid.UUID
		for rows.Next() {
			var id, alvoID uuid.UUID
			if err := rows.Scan(&id, &alvoID); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if len(ids) != 1 || ids[0] != notifAlvo {
			t.Errorf("ClaimDueNotifications antiga devolveu %v, queria só a do alvo", ids)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("ClaimDueNotifications antiga: %v", err)
	}
	var tentativas int
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT attempts FROM notifications WHERE id = $1`, notifHB).Scan(&tentativas)
	})
	if err != nil || tentativas != 1 {
		t.Errorf("a notificação de heartbeat ficou com %d tentativas (erro %v), queria 1", tentativas, err)
	}

	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, antigaListReminderCandidates)
		if err != nil {
			return err
		}
		defer rows.Close()
		var incidentes []uuid.UUID
		for rows.Next() {
			var incidente, c uuid.UUID
			if err := rows.Scan(&incidente, &c); err != nil {
				return err
			}
			incidentes = append(incidentes, incidente)
		}
		if len(incidentes) != 1 || incidentes[0] != incidenteAlvo {
			t.Errorf("ListReminderCandidates antiga devolveu %v, queria só o do alvo", incidentes)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("ListReminderCandidates antiga: %v", err)
	}

	err = emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		var id, alvoID uuid.UUID
		var nome, causa string
		var inicio time.Time
		var fim, ack *time.Time
		err := tx.QueryRow(ctx, antigaAckIncident, agora, dono, incidenteHB).
			Scan(&id, &alvoID, &nome, &inicio, &fim, &causa, &ack)
		if err != pgx.ErrNoRows {
			t.Errorf("AckIncident antiga no incidente de heartbeat deu %v, queria sem linha", err)
		}
		return tx.QueryRow(ctx, antigaAckIncident, agora, dono, incidenteAlvo).
			Scan(&id, &alvoID, &nome, &inicio, &fim, &causa, &ack)
	})
	if err != nil {
		t.Fatalf("AckIncident antiga: %v", err)
	}
}

// abrirNoAlvoNovo prova que o INSERT antigo de incidente passa no arco.
func abrirNoAlvoNovo(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID) {
	t.Helper()
	alvo := novoAlvo(t, pool, dono, agora)
	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), antigaOpenIncident, novoID(), alvo, agora, "dns")
		return err
	})
	if err != nil {
		t.Errorf("OpenIncident antiga: %v", err)
	}
}

func marcarAberturaAvisada(t *testing.T, pool *pgxpool.Pool, incidentes ...uuid.UUID) {
	t.Helper()
	err := storage.InProberTx(context.Background(), pool, func(q *db.Queries) error {
		for _, id := range incidentes {
			if _, err := q.MarkOpenNotified(context.Background(), db.MarkOpenNotifiedParams{ID: id, Now: agora}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// migrationsAte lê os arquivos reais até version, como a imagem daquela época
// os embutia.
func migrationsAte(t *testing.T, version string) fstest.MapFS {
	t.Helper()
	arquivos, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for _, a := range arquivos {
		nome := filepath.Base(a)
		if strings.Compare(nome, version) >= 0 {
			continue
		}
		b, err := os.ReadFile(a)
		if err != nil {
			t.Fatal(err)
		}
		fsys[nome] = &fstest.MapFile{Data: b}
	}
	return fsys
}

// O boot da imagem anterior roda as migrations dela num banco que já está à
// frente: tem de ser um no-op, ou o rollback nem sobe. O provider é montado
// como em storage.Migrate.
func TestOlderBinaryBootsOnNewerSchema(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	anterior, err := goose.NewProvider(goose.DialectPostgres, conn, migrationsAte(t, "00016"))
	if err != nil {
		t.Fatal(err)
	}
	aplicadas, err := anterior.Up(ctx)
	if err != nil {
		t.Fatalf("boot do binário anterior: %v", err)
	}
	if len(aplicadas) != 0 {
		t.Errorf("o binário anterior aplicou %d migrations", len(aplicadas))
	}
}

// A lacuna da 00015 é permanente: quem chegar depois tem de numerar acima da
// maior versão, ou o boot cai (spec de heartbeats, §2). Este teste recusa um
// arquivo 00015, em vez de deixar a surpresa para o deploy.
func TestMigration00015StaysVacant(t *testing.T) {
	if m, _ := filepath.Glob("migrations/00015*"); len(m) != 0 {
		t.Fatalf("%v: storage.Migrate recusa a 00015 num banco na 00016; use o próximo número livre", m)
	}
}
