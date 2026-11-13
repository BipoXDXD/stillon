package storage_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// ListPublicLatency soma rollups de [since, cut) e checks brutos de
// [greatest(cut, since), until). O bucket do rollup é hora cheia (CHECK da
// 00006), então as bordas se provam movendo o parâmetro um microssegundo,
// que é a resolução do timestamptz, em volta do instante gravado.

const umMicro = time.Microsecond

// novoRollup grava a hora consolidada de um alvo com p50 = p95 = p99 = max,
// que satisfaz os CHECKs de latência da 00013.
func novoRollup(t *testing.T, conn *pgx.Conn, alvo uuid.UUID, bucket time.Time, p50, ok int32) {
	t.Helper()
	if _, err := conn.Exec(context.Background(),
		`INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		 VALUES ($1, $2, $3, $3, $4, $4, $4, $4)`, alvo, bucket, ok, p50); err != nil {
		t.Fatalf("gravar rollup: %v", err)
	}
}

// novoCheckCom grava um check com latência e status escolhidos. A queda leva
// um motivo, que a tabela exige só de quem caiu.
func novoCheckCom(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID, em time.Time, status string, latencia int32) {
	t.Helper()
	ctx := context.Background()
	var motivo *string
	if status == "down" {
		timeout := "timeout"
		motivo = &timeout
	}
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: novoID(), TargetID: alvo, CheckedAt: em, Status: status, LatencyMS: latencia, ErrorKind: motivo,
		})
	})
	if err != nil {
		t.Fatalf("gravar check: %v", err)
	}
}

// latencias lê a página sem RLS: só o JOIN da própria query separa os alvos.
func latencias(t *testing.T, q *db.Queries, p db.ListPublicLatencyParams) []db.ListPublicLatencyRow {
	t.Helper()
	l, err := q.ListPublicLatency(context.Background(), p)
	if err != nil {
		t.Fatalf("ListPublicLatency(%+v): %v", p, err)
	}
	return l
}

type casoDeBorda struct {
	nome              string
	since, cut, until time.Time
	querLinha         bool
}

func conferirBordas(t *testing.T, q *db.Queries, pagina, alvo uuid.UUID, p50 int32, casos []casoDeBorda) {
	t.Helper()
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := latencias(t, q, db.ListPublicLatencyParams{PageID: pagina, Since: c.since, Cut: c.cut, Until: c.until})
			var want []db.ListPublicLatencyRow
			if c.querLinha {
				want = []db.ListPublicLatencyRow{{TargetID: alvo, P50: p50}}
			}
			if !slices.Equal(got, want) {
				t.Errorf("leu %v, queria %v", got, want)
			}
		})
	}
}

func TestListPublicLatencyRollupWindowEdges(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	pagina := novaPagina(t, pool, dono, "pagina-a", true, alvo)
	conn := conexaoSemRLS(t)
	hora := agora.Add(-2 * time.Hour)
	novoRollup(t, conn, alvo, hora, 100, 1)
	q := db.New(conn)
	desde, corte, ate := agora.Add(-24*time.Hour), agora, agora.Add(time.Hour)

	conferirBordas(t, q, pagina, alvo, 100, []casoDeBorda{
		{"since no bucket entra", hora, corte, ate, true},
		{"since um micro depois do bucket sai", hora.Add(umMicro), corte, ate, false},
		{"cut um micro depois do bucket entra", desde, hora.Add(umMicro), ate, true},
		{"cut no bucket sai", desde, hora, ate, false},
	})
}

func TestListPublicLatencyRawWindowEdges(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	pagina := novaPagina(t, pool, dono, "pagina-a", true, alvo)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	em := agora.Add(10 * time.Minute)
	novoCheckCom(t, pool, alvo, em, "up", 12)
	q := queriesSemRLS(t)
	desde, antes, depois := agora.Add(-24*time.Hour), agora.Add(-time.Hour), agora.Add(time.Hour)

	conferirBordas(t, q, pagina, alvo, 12, []casoDeBorda{
		{"cut no check entra", desde, em, depois, true},
		{"cut um micro depois do check sai", desde, em.Add(umMicro), depois, false},
		{"since depois do cut e no check entra", em, antes, depois, true},
		{"since depois do cut e um micro depois do check sai", em.Add(umMicro), antes, depois, false},
		{"until um micro depois do check entra", desde, antes, em.Add(umMicro), true},
		{"until no check sai", desde, antes, em, false},
	})
}

func TestListPublicLatencyWithoutSuccessesReturnsNoRow(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	vazio := novoAlvo(t, pool, dono, agora)
	soQuedas := novoAlvo(t, pool, dono, agora)
	pagina := novaPagina(t, pool, dono, "pagina-a", true, vazio, soQuedas)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	novoCheckCom(t, pool, soQuedas, agora.Add(time.Minute), "down", 30)
	novoCheckCom(t, pool, soQuedas, agora.Add(2*time.Minute), "down", 40)
	q := queriesSemRLS(t)

	got := latencias(t, q, db.ListPublicLatencyParams{
		PageID: pagina, Since: agora.Add(-24 * time.Hour), Cut: agora, Until: agora.Add(time.Hour),
	})
	if len(got) != 0 {
		t.Errorf("sem sucesso na janela, leu %v; queria nenhuma linha", got)
	}
}

func TestListPublicLatencyIgnoresTargetsOfAnotherPage(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA, alvoB := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, b, agora)
	paginaA := novaPagina(t, pool, a, "pagina-a", true, alvoA)
	novaPagina(t, pool, b, "pagina-b", true, alvoB)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	conn := conexaoSemRLS(t)
	hora := agora.Add(-2 * time.Hour)
	novoRollup(t, conn, alvoA, hora, 100, 1)
	novoRollup(t, conn, alvoB, hora, 500, 1)
	novoCheckCom(t, pool, alvoB, agora.Add(time.Minute), "up", 900)

	got := latencias(t, db.New(conn), db.ListPublicLatencyParams{
		PageID: paginaA, Since: agora.Add(-24 * time.Hour), Cut: agora, Until: agora.Add(time.Hour),
	})
	want := []db.ListPublicLatencyRow{{TargetID: alvoA, P50: 100}}
	if !slices.Equal(got, want) {
		t.Errorf("a página de A leu %v, queria só %v", got, want)
	}
}

// A média é ponderada pelos sucessos de cada hora; a hora bruta usa a
// mediana discreta só dos checks que não caíram, e cada hora conta à parte.
func TestListPublicLatencyWeightsHourlyMediansBySuccesses(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	pagina := novaPagina(t, pool, dono, "pagina-a", true, alvo)
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
	conn := conexaoSemRLS(t)
	novoRollup(t, conn, alvo, agora.Add(-3*time.Hour), 100, 1)
	novoRollup(t, conn, alvo, agora.Add(-2*time.Hour), 40, 3)
	// Hora de 12h: sucessos 10, 20 e 30 (mediana 20, peso 3) e uma queda.
	novoCheckCom(t, pool, alvo, agora.Add(1*time.Minute), "up", 10)
	novoCheckCom(t, pool, alvo, agora.Add(2*time.Minute), "up", 20)
	novoCheckCom(t, pool, alvo, agora.Add(3*time.Minute), "up", 30)
	novoCheckCom(t, pool, alvo, agora.Add(4*time.Minute), "down", 1)
	// Hora de 13h: um sucesso de 70.
	novoCheckCom(t, pool, alvo, agora.Add(70*time.Minute), "up", 70)

	got := latencias(t, db.New(conn), db.ListPublicLatencyParams{
		PageID: pagina, Since: agora.Add(-24 * time.Hour), Cut: agora, Until: agora.Add(2 * time.Hour),
	})
	// (100*1 + 40*3 + 20*3 + 70*1) / 8 = 350 / 8 = 43,75. sum de bigint é
	// numeric no Postgres, e o ::int arredonda: 44.
	want := []db.ListPublicLatencyRow{{TargetID: alvo, P50: 44}}
	if !slices.Equal(got, want) {
		t.Errorf("leu %v, queria %v", got, want)
	}
}
