package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const migrationLogRun = "heartbeat_events_log_run"

// O SQL do binário anterior (o piso de rollback), copiado do pacote db daquela
// versão. Não atualize junto com as queries: a imagem anterior não muda, e é
// ela que volta a gravar sobre o schema novo num rollback.
const (
	pisoInsertHeartbeatEvent = `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind, runtime_ms, exit_code)
VALUES ($1, $2, $3, $4, $5, $6)`

	pisoSaveHeartbeatPing = `UPDATE heartbeats
   SET deadline_at = $1, last_ping_at = $2,
       last_success_at = $3, last_start_at = $4,
       last_signal = $5, failing_since = $6,
       last_runtime_ms = $7
 WHERE id = $8`
)

// O binário anterior não cita as colunas novas: os DEFAULTs têm de fazer o
// INSERT dele gravar um sinal de ping sem execução, e o UPDATE dele não pode
// apagar o que o binário novo gravou. Os CHECKs seguem valendo para o que é
// novo.
func TestLogRunMigrationDefaultsKeepTheFloorWorking(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	hb := novoHeartbeat(t, pool, novoUsuario(t, pool), "piso")
	rid := novoID()

	evento := novoID()
	comoSondador(t, pool, pisoInsertHeartbeatEvent, evento, hb, agora, "success", int64(1500), int16(0))
	var origem string
	var execucao *uuid.UUID
	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT source, run_id FROM heartbeat_events WHERE id = $1`, evento).
			Scan(&origem, &execucao)
	})
	if err != nil {
		t.Fatalf("ler o evento do piso: %v", err)
	}
	if origem != "ping" || execucao != nil {
		t.Errorf("evento do piso com source=%q e run_id=%v, queria ping e NULL", origem, execucao)
	}

	comoSondador(t, pool, `UPDATE heartbeats SET last_run_id = $1, ping_resumes = true WHERE id = $2`, rid, hb)
	comoSondador(t, pool, pisoSaveHeartbeatPing,
		agora.Add(2*time.Hour), agora, agora, nil, "success", nil, int64(1500), hb)
	var ultima *uuid.UUID
	var retoma bool
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT last_run_id, ping_resumes FROM heartbeats WHERE id = $1`, hb).
			Scan(&ultima, &retoma)
	})
	if err != nil {
		t.Fatalf("ler o heartbeat: %v", err)
	}
	if ultima == nil || *ultima != rid || !retoma {
		t.Errorf("depois do UPDATE do piso: last_run_id=%v e ping_resumes=%v, queria %s e true", ultima, retoma, rid)
	}

	comoSondador(t, pool, `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind, run_id) VALUES ($1, $2, $3, 'log', $4)`,
		novoID(), hb, agora, rid)

	casos := []struct {
		nome, sql, restricao string
		args                 []any
	}{
		{"log com exit_code", pisoInsertHeartbeatEvent, "heartbeat_events_exit_code_check",
			[]any{novoID(), hb, agora, "log", nil, int16(3)}},
		{"source fora da lista",
			`INSERT INTO heartbeat_events (id, heartbeat_id, at, kind, source) VALUES ($1, $2, $3, 'success', 'cron')`,
			"heartbeat_events_source_check", []any{novoID(), hb, agora}},
		{"last_signal log", `UPDATE heartbeats SET last_signal = 'log' WHERE id = $1`,
			"heartbeats_last_signal_check", []any{hb}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, c.sql, c.args...)
				return err
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != c.restricao {
				t.Errorf("deu %v, queria 23514 em %s", err, c.restricao)
			}
		})
	}
}

// A descida apaga o que o schema antigo não sabe guardar (os eventos log) e
// devolve o CHECK antigo; o resto do log fica. Subir de novo funciona.
func TestLogRunMigrationDownRemovesLogsAndColumns(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, migrationLogRun)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	hb := novoHeartbeat(t, pool, novoUsuario(t, pool), "descida")
	rid := novoID()
	const inserir = `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind, run_id, source) VALUES ($1, $2, $3, $4, $5, $6)`
	comoSondador(t, pool, inserir, novoID(), hb, agora, "start", rid, "ping")
	comoSondador(t, pool, inserir, novoID(), hb, agora, "log", rid, "ping")
	comoSondador(t, pool, inserir, novoID(), hb, agora, "success", rid, "ping")
	comoSondador(t, pool, inserir, novoID(), hb, agora, "success", nil, "panel")
	comoSondador(t, pool, `UPDATE heartbeats SET last_run_id = $1, ping_resumes = true WHERE id = $2`, rid, hb)

	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %s: %v", migrationLogRun, err)
	}

	var tipos []string
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT kind FROM heartbeat_events ORDER BY id`)
		if err != nil {
			return err
		}
		tipos, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		t.Fatalf("ler eventos depois do down: %v", err)
	}
	if !slices.Equal(tipos, []string{"start", "success", "success"}) {
		t.Errorf("eventos depois do down: %v, queria [start success success]", tipos)
	}

	var colunas int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE (table_name = 'heartbeat_events' AND column_name IN ('run_id', 'source'))
		   OR (table_name = 'heartbeats' AND column_name IN ('last_run_id', 'ping_resumes'))`).Scan(&colunas); err != nil {
		t.Fatal(err)
	}
	if colunas != 0 {
		t.Errorf("sobraram %d colunas novas depois do down", colunas)
	}
	var indices int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'heartbeat_events_run_idx'`).
		Scan(&indices); err != nil {
		t.Fatal(err)
	}
	if indices != 0 {
		t.Errorf("o índice heartbeat_events_run_idx sobrou depois do down")
	}
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind) VALUES ($1, $2, $3, 'log')`,
			novoID(), hb, agora)
		return err
	})
	if codigoPG(err) != "23514" {
		t.Errorf("evento log depois do down deu %v, queria 23514", err)
	}

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Errorf("subir de novo depois do down: %v", err)
	}
}

// O boot do binário anterior (as migrations até a véspera desta) num banco que
// já tem a +1 tem de ser um no-op, ou o rollback nem sobe. O piso de rollback
// sobre o schema completo é o TestFloorBinaryBootsOnHeartbeatLimits.
func TestPreviousBinaryBootsOnLogRun(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, migrationLogRun)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	piso, err := goose.NewProvider(goose.DialectPostgres, conn, migrationsAte(t, fmt.Sprintf("%05d", versao)))
	if err != nil {
		t.Fatal(err)
	}
	aplicadas, err := piso.Up(ctx)
	if err != nil {
		t.Fatalf("boot do binário do piso: %v", err)
	}
	if len(aplicadas) != 0 {
		t.Errorf("o binário do piso aplicou %d migrations", len(aplicadas))
	}
}

// comoSondador executa um comando no escopo do sondador e falha o teste se
// ele der erro.
func comoSondador(t *testing.T, pool *pgxpool.Pool, comando string, args ...any) {
	t.Helper()
	err := emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), comando, args...)
		return err
	})
	if err != nil {
		t.Fatalf("%s: %v", comando, err)
	}
}
