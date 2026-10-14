package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// E3 da revisão de 2026-10-14. Com dois sondadores, duas consolidações
// concorrentes fariam o mesmo upsert em ordens diferentes (deadlock 40P01) ou
// em dobro. Quem chega com a trava tomada pula a rodada na hora, sem esperar
// nem gravar; com a trava solta, a rodada seguinte consolida.
func TestConsolidationSkipsTheRoundWhileAnotherHoldsTheLock(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	checkEm(t, pool, alvo, hora(10, 5), "up", 100)

	outra, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer outra.Rollback(context.Background())
	if _, err := outra.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, storage.RollupLockKey); err != nil {
		t.Fatal(err)
	}

	n, err := storage.ConsolidateRollups(ctx, pool, hora(11, 30))
	if err != nil {
		t.Fatalf("consolidar com a trava tomada: %v; queria pular a rodada sem esperar", err)
	}
	if n != 0 || len(rollupsDe(t, pool, dono, alvo)) != 0 {
		t.Fatalf("gravou %d rollups com outra consolidação em curso; queria pular", n)
	}

	if err := outra.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	n, err = storage.ConsolidateRollups(ctx, pool, hora(11, 30))
	if err != nil || n != 1 {
		t.Fatalf("depois de soltar a trava: %d rollups, %v; queria 1", n, err)
	}
}
