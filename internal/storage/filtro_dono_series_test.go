package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A série longa de um alvo (GET /targets/{id}/checks acima de 48 h) junta os
// rollups consolidados e as horas cruas ainda não consolidadas. As duas
// queries recebem o id do alvo e filtram o dono pelo JOIN com targets.

func TestListRollupsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	if _, err := conn.Exec(ctx, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, 2, 2, 10, 20, 30, 40)`, alvoA, agora); err != nil {
		t.Fatal(err)
	}
	rollups := func(dono uuid.UUID) []db.ListRollupsRow {
		t.Helper()
		r, err := q.ListRollups(ctx, db.ListRollupsParams{
			UserID: dono, TargetID: alvoA, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	if deB := rollups(b); len(deB) != 0 {
		t.Errorf("B leu %d rollups do alvo de A sem RLS, queria 0", len(deB))
	}
	deA := rollups(a)
	if len(deA) != 1 || !deA[0].Bucket.Equal(agora) || deA[0].Total != 2 {
		t.Errorf("A leu %v, queria um rollup de total 2 em %s", deA, agora)
	}
}

func TestListRawHoursOfTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvoA, agora.Add(5*time.Minute))
	q := queriesSemRLS(t)
	horas := func(dono uuid.UUID) []db.ListRawHoursRow {
		t.Helper()
		r, err := q.ListRawHours(ctx, db.ListRawHoursParams{
			UserID: dono, TargetID: &alvoA, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	if deB := horas(b); len(deB) != 0 {
		t.Errorf("B leu %d horas cruas do alvo de A sem RLS, queria 0", len(deB))
	}
	deA := horas(a)
	if len(deA) != 1 || deA[0].TargetID != alvoA || !deA[0].Bucket.Equal(agora) || deA[0].Total != 1 {
		t.Errorf("A leu %v, queria uma hora de total 1 do alvo %s em %s", deA, alvoA, agora)
	}
}
