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

// alvosVencidosNoProjeto grava n alvos no projeto, com next_run_at em
// primeiro, primeiro+passo, primeiro+2·passo…, por fora da RLS e numa
// instrução só (generate_series): mil alvos um a um levariam segundos.
func alvosVencidosNoProjeto(t *testing.T, projeto, titular uuid.UUID, n int, primeiro time.Time, passo time.Duration) []uuid.UUID {
	t.Helper()
	linhas, err := testdb.Admin(t).Query(context.Background(), `
		INSERT INTO targets (id, user_id, project_id, name, url, method, interval_sec, timeout_ms,
		                     expected_status, enabled, next_run_at, created_at)
		SELECT gen_random_uuid(), $2, $1, 'alvo', 'https://exemplo.com', 'GET', 60, 5000, 200, true,
		       $3::timestamptz + make_interval(secs => (g - 1) * $4::float8), $5
		  FROM generate_series(1, $6::int) AS g
		RETURNING id`,
		projeto, titular, primeiro, passo.Seconds(), agora, n)
	if err != nil {
		t.Fatalf("criar alvos vencidos: %v", err)
	}
	defer linhas.Close()
	var ids []uuid.UUID
	for linhas.Next() {
		var id uuid.UUID
		if err := linhas.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := linhas.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func reivindicar(t *testing.T, pool *pgxpool.Pool, max int32) []db.ClaimDueTargetsRow {
	t.Helper()
	ctx := context.Background()
	var lote []db.ClaimDueTargetsRow
	if err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		var err error
		lote, err = q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: max})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return lote
}

func porProjeto(lote []db.ClaimDueTargetsRow) map[uuid.UUID]int {
	n := map[uuid.UUID]int{}
	for _, a := range lote {
		if a.ProjectID != nil {
			n[*a.ProjectID]++
		}
	}
	return n
}

// Um projeto com mil alvos atrasados não pode tomar todas as vagas da rodada:
// o alvo do outro projeto entra no primeiro lote, mesmo vencido há menos tempo.
func TestClaimInterleavesProjects(t *testing.T) {
	pool := testdb.New(t)
	donoA, donoB := novoUsuario(t, pool), novoUsuario(t, pool)
	projetoA, projetoB := testdb.ProjetoPadrao(t, donoA), testdb.ProjetoPadrao(t, donoB)
	alvosVencidosNoProjeto(t, projetoA, donoA, 1000, agora.Add(-time.Hour), time.Millisecond)
	doB := alvosVencidosNoProjeto(t, projetoB, donoB, 1, agora, 0)[0]

	lote := reivindicar(t, pool, 10)

	if len(lote) != 10 {
		t.Fatalf("lote de %d alvos; queria 10", len(lote))
	}
	for _, a := range lote {
		if a.ID == doB {
			return
		}
	}
	t.Errorf("o alvo do projeto B ficou fora do lote: %v", porProjeto(lote))
}

// Dentro do projeto, a ordem continua sendo a do vencimento.
func TestClaimKeepsOldestFirstWithinProject(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	ids := alvosVencidosNoProjeto(t, testdb.ProjetoPadrao(t, dono), dono, 3, agora.Add(-3*time.Minute), time.Minute)

	lote := reivindicar(t, pool, 2)

	vistos := map[uuid.UUID]bool{}
	for _, a := range lote {
		vistos[a.ID] = true
	}
	if len(lote) != 2 || !vistos[ids[0]] || !vistos[ids[1]] {
		t.Errorf("reivindicou %v; queria os dois mais antigos %v", vistos, ids[:2])
	}
}

// Um de cada projeto por vez: A 5, B 5 e C 1 com seis vagas dão dois de A,
// dois de B, o de C e, na terceira vez, o mais antigo entre A e B (o de A).
func TestClaimRoundRobinAcrossThreeProjects(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	projetoA := testdb.ProjetoPadrao(t, dono)
	projetoB := testdb.NovoProjeto(t, dono, "B")
	projetoC := testdb.NovoProjeto(t, dono, "C")
	alvosVencidosNoProjeto(t, projetoA, dono, 5, agora.Add(-time.Hour), time.Minute)
	alvosVencidosNoProjeto(t, projetoB, dono, 5, agora.Add(-30*time.Minute), time.Minute)
	alvosVencidosNoProjeto(t, projetoC, dono, 1, agora, 0)

	n := porProjeto(reivindicar(t, pool, 6))

	if n[projetoA] != 3 || n[projetoB] != 2 || n[projetoC] != 1 {
		t.Errorf("lote por projeto: A %d, B %d, C %d; queria 3, 2 e 1", n[projetoA], n[projetoB], n[projetoC])
	}
}

// A janela de row_number roda sem trava; quem impede o lote em dobro é a
// re-trava do lote. O primeiro sondador segura 10 alvos sem commit; o segundo
// pede 20 e não pode levar nenhum dos 10, nem quando o primeiro faz commit
// com o segundo esperando.
func TestClaimDoesNotDoubleClaimConcurrently(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvosVencidosNoProjeto(t, testdb.ProjetoPadrao(t, dono), dono, 20, agora.Add(-time.Minute), time.Second)

	tx1, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback(ctx)
	if _, err := tx1.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	primeiros, err := db.New(tx1).ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(primeiros) != 10 {
		t.Fatalf("o primeiro sondador reivindicou %d, queria 10", len(primeiros))
	}

	type resultado struct {
		lote []db.ClaimDueTargetsRow
		err  error
	}
	fim := make(chan resultado, 1)
	go func() {
		var lote []db.ClaimDueTargetsRow
		err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
			var err error
			lote, err = q.ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 20})
			return err
		})
		fim <- resultado{lote, err}
	}()
	testdb.EsperarBloqueados(t, pool, 1, fim)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-fim
	if r.err != nil {
		t.Fatal(r.err)
	}

	vistos := map[uuid.UUID]bool{}
	for _, a := range primeiros {
		vistos[a.ID] = true
	}
	for _, a := range r.lote {
		if vistos[a.ID] {
			t.Errorf("alvo %s reivindicado pelos dois sondadores", a.ID)
		}
	}
	if len(primeiros)+len(r.lote) != 20 {
		t.Errorf("reivindicados %d+%d, queria 20 no total", len(primeiros), len(r.lote))
	}
}

func TestClaimReturnsProjectID(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	projeto := testdb.NovoProjeto(t, dono, "outro")
	alvosVencidosNoProjeto(t, projeto, dono, 1, agora.Add(-time.Minute), 0)

	lote := reivindicar(t, pool, 1)

	if len(lote) != 1 || lote[0].ProjectID == nil || *lote[0].ProjectID != projeto {
		t.Errorf("lote %+v; queria um alvo do projeto %s", lote, projeto)
	}
}
