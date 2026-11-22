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

// Espelho de filtro_dono_test.go para as leituras de GET /targets/summary:
// superusuário, sem RLS, e só o project_id do WHERE separa A de B, dois
// projetos do MESMO usuário (o filtro por dono deixaria passar).

func TestListTargetSummariesFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)
	listar := func(projeto uuid.UUID) []db.ListTargetSummariesRow {
		t.Helper()
		s, err := q.ListTargetSummaries(ctx, projeto)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	if deB := listar(pb); len(deB) != 0 {
		t.Errorf("B leu o resumo de %d alvos sem RLS, queria 0 (o alvo de A vazou)", len(deB))
	}
	if deA := listar(pa); len(deA) != 1 || deA[0].ID != alvoA {
		t.Errorf("A leu o resumo %v, queria só o alvo %s", deA, alvoA)
	}
}

func TestListSummaryRollupsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	conn := conexaoSemRLS(t)
	q := db.New(conn)
	if _, err := conn.Exec(ctx, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, 2, 2, 10, 20, 30, 40)`, alvoA, agora); err != nil {
		t.Fatal(err)
	}
	rollups := func(projeto uuid.UUID) []db.ListSummaryRollupsRow {
		t.Helper()
		r, err := q.ListSummaryRollups(ctx, db.ListSummaryRollupsParams{
			ProjectID: projeto, FromAt: agora.Add(-time.Hour), ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	if deB := rollups(pb); len(deB) != 0 {
		t.Errorf("B leu %d rollups do resumo de A sem RLS, queria 0", len(deB))
	}
	deA := rollups(pa)
	if len(deA) != 1 || deA[0].TargetID != alvoA || !deA[0].Bucket.Equal(agora) || deA[0].Total != 2 {
		t.Errorf("A leu %v, queria um rollup de total 2 do alvo %s em %s", deA, alvoA, agora)
	}
}

func TestListSummaryCurrentHourFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvoA, agora.Add(5*time.Minute))
	q := queriesSemRLS(t)
	contagens := func(projeto uuid.UUID) []db.ListSummaryCurrentHourRow {
		t.Helper()
		r, err := q.ListSummaryCurrentHour(ctx, db.ListSummaryCurrentHourParams{
			ProjectID: projeto, FromAt: agora, ToAt: agora.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	if deB := contagens(pb); len(deB) != 0 {
		t.Errorf("B leu %d contagens da hora corrente de A sem RLS, queria 0", len(deB))
	}
	deA := contagens(pa)
	if len(deA) != 1 || deA[0].TargetID != alvoA || deA[0].Total != 1 || deA[0].Ok != 1 {
		t.Errorf("A leu %v, queria uma contagem de total 1 do alvo %s", deA, alvoA)
	}
}
