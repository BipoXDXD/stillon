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
// queries recebem o id do alvo e filtram o projeto pelo JOIN com targets.

func TestListRollupsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	// A e B são dois projetos do MESMO usuário: só o filtro por projeto os
	// separa (o por dono deixaria passar).
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	if _, err := conn.Exec(ctx, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, 2, 2, 10, 20, 30, 40)`, alvoA, agora); err != nil {
		t.Fatal(err)
	}
	rollups := func(projeto uuid.UUID) []db.ListRollupsRow {
		t.Helper()
		r, err := q.ListRollups(ctx, db.ListRollupsParams{
			ProjectID: projeto, TargetID: alvoA, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	if deB := rollups(pb); len(deB) != 0 {
		t.Errorf("B leu %d rollups do alvo de A sem RLS, queria 0", len(deB))
	}
	deA := rollups(pa)
	if len(deA) != 1 || !deA[0].Bucket.Equal(agora) || deA[0].Total != 2 {
		t.Errorf("A leu %v, queria um rollup de total 2 em %s", deA, agora)
	}
}

func TestListRawHoursOfTargetFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	// A e B são dois projetos do MESMO usuário: só o filtro por projeto os
	// separa (o por dono deixaria passar).
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvoA, agora.Add(5*time.Minute))
	q := queriesSemRLS(t)
	horas := func(projeto uuid.UUID) []db.ListRawHoursRow {
		t.Helper()
		r, err := q.ListRawHours(ctx, db.ListRawHoursParams{
			ProjectID: projeto, TargetID: &alvoA, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	if deB := horas(pb); len(deB) != 0 {
		t.Errorf("B leu %d horas cruas do alvo de A sem RLS, queria 0", len(deB))
	}
	deA := horas(pa)
	if len(deA) != 1 || deA[0].TargetID != alvoA || !deA[0].Bucket.Equal(agora) || deA[0].Total != 1 {
		t.Errorf("A leu %v, queria uma hora de total 1 do alvo %s em %s", deA, alvoA, agora)
	}
}

// A marca d'água do resumo é a do projeto: sem o filtro, o rollup de A
// adiantaria a de B, e B pularia as próprias horas ainda não consolidadas.
func TestLatestProjectRollupBucketFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	// A e B são dois projetos do MESMO usuário: só o filtro por projeto os
	// separa (o por dono deixaria passar).
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	hora := agora.Add(-time.Hour)
	if _, err := conn.Exec(ctx, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, 2, 2, 10, 20, 30, 40)`, alvoA, hora); err != nil {
		t.Fatal(err)
	}
	marca := func(projeto uuid.UUID) time.Time {
		t.Helper()
		m, err := q.LatestProjectRollupBucket(ctx, db.LatestProjectRollupBucketParams{Before: agora, ProjectID: projeto})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	if m := marca(pb); !m.Equal(time.Unix(0, 0)) {
		t.Errorf("a marca d'água de B sem RLS foi %s, queria a época Unix (o rollup de A vazou)", m.UTC().Format(time.RFC3339))
	}
	if m := marca(pa); !m.Equal(hora) {
		t.Errorf("a marca d'água de A foi %s, queria %s", m.UTC().Format(time.RFC3339), hora.UTC().Format(time.RFC3339))
	}
}
