package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Review Focus 2. 23:30 do dia 30 em São Paulo já é dia 1 em UTC.
func TestEnsureChecksPartitionsUsesUTCMonths(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	sp := time.FixedZone("BRT", -3*60*60)

	if err := storage.EnsureChecksPartitions(ctx, pool, time.Date(2026, 9, 30, 23, 30, 0, 0, sp)); err != nil {
		t.Fatal(err)
	}
	for nome, quer := range map[string]bool{
		"checks_2026_09": false,
		"checks_2026_10": true,
		"checks_2026_11": true,
	} {
		var existe bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1 AND relispartition)`, nome).
			Scan(&existe); err != nil {
			t.Fatal(err)
		}
		if existe != quer {
			t.Errorf("partição %s existe=%v, queria %v", nome, existe, quer)
		}
	}

	// Idempotente: o tick diário chama de novo sem erro.
	if err := storage.EnsureChecksPartitions(ctx, pool, time.Date(2026, 9, 30, 23, 30, 0, 0, sp)); err != nil {
		t.Errorf("segunda chamada falhou: %v", err)
	}
}

// Review Focus 2, pelo lado do banco. A função é chamada numa sessão com
// TimeZone de São Paulo; as bordas precisam ser meia-noite UTC.
func TestPartitionBoundsAreUTCRegardlessOfSessionTimeZone(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SET TimeZone = 'America/Sao_Paulo'`,
		`SELECT ensure_checks_partition('2026-09-15')`,
		`SELECT ensure_checks_partition('2026-10-15')`,
		`RESET TimeZone`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Release()

	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	novoCheck(t, pool, alvo, time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC))
	novoCheck(t, pool, alvo, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT tableoid::regclass::text FROM checks ORDER BY checked_at`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"checks_2026_09", "checks_2026_10"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("partições = %v, queria %v: as bordas seguiram o fuso da sessão", got, want)
	}
}
