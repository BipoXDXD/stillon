package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Uma criação concorrente interrompida deixa o índice inválido no catálogo.
// O próximo boot precisa substituir esse índice e concluir a migration.
func TestHeartbeatEventRetentionIndexRecoversFromInterruptedBuild(t *testing.T) {
	dsn := testdb.EmptyDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := storage.MigrateUpTo(ctx, dsn, 36); err != nil {
		t.Fatal(err)
	}

	writer, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(context.Background())
	tx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `LOCK TABLE heartbeat_events IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}

	builder, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close(context.Background())
	if _, err := builder.Exec(ctx, `SET lock_timeout = '1s'`); err != nil {
		t.Fatal(err)
	}
	_, err = builder.Exec(ctx, `CREATE INDEX CONCURRENTLY heartbeat_events_at_idx ON heartbeat_events (at)`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("criação interrompida: erro %v, queria lock_timeout (55P03)", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	validade := func() bool {
		t.Helper()
		var valido bool
		if err := builder.QueryRow(ctx, `SELECT indisvalid FROM pg_index
			WHERE indexrelid = 'heartbeat_events_at_idx'::regclass`).Scan(&valido); err != nil {
			t.Fatalf("consultar validade do índice: %v", err)
		}
		return valido
	}
	if validade() {
		t.Fatal("a criação interrompida deveria ter deixado o índice inválido")
	}
	if err := storage.MigrateUpTo(ctx, dsn, 37); err != nil {
		t.Fatalf("migration 00037 não recuperou o índice inválido: %v", err)
	}
	if !validade() {
		t.Fatal("a migration concluiu com o índice ainda inválido")
	}
}
