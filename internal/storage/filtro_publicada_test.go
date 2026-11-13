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

// As leituras da status page pública (statuspage.projetar, sob InPublicTx)
// recebem o id da página e confirmam no próprio SQL que ela está publicada:
// a RLS de InPublicTx não é a única barreira. Os testes rodam como
// superusuário, sem RLS, e cada consulta lê uma página publicada (controle,
// para o vazio não passar por acidente) e um rascunho com os mesmos dados.

// paginaComDados cria a página com um alvo que tem rollup, check bruto e
// incidente resolvido dentro da janela que os testes consultam.
func paginaComDados(t *testing.T, publicada bool, slug string) (queries *db.Queries, pagina uuid.UUID) {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	pagina = novaPagina(t, pool, dono, slug, publicada, alvo)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvo, agora.Add(-10*time.Minute))
	conn := conexaoSemRLS(t)
	if _, err := conn.Exec(ctx, `INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
		VALUES ($1, $2, 2, 2, 10, 20, 30, 40)`, alvo, agora.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO incidents (id, target_id, started_at, resolved_at, cause)
		VALUES ($1, $2, $3, $4, 'timeout')`, novoID(), alvo, agora.Add(-time.Hour), agora.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return db.New(conn), pagina
}

// leiturasPublicas conta o que cada consulta pública devolve para a página.
// A marca d'água conta 1 quando não é a época Unix (há rollup visível).
func leiturasPublicas(t *testing.T, q *db.Queries, pagina uuid.UUID) map[string]int {
	t.Helper()
	ctx := context.Background()
	desde, corte, ate := agora.Add(-24*time.Hour), agora.Add(-time.Hour), agora.Add(time.Hour)
	n := map[string]int{}

	itens, err := q.ListPublicItems(ctx, pagina)
	if err != nil {
		t.Fatal(err)
	}
	n["ListPublicItems"] = len(itens)

	marca, err := q.LatestPageRollupBucket(ctx, db.LatestPageRollupBucketParams{Before: agora, PageID: pagina})
	if err != nil {
		t.Fatal(err)
	}
	if !marca.Equal(time.Unix(0, 0)) {
		n["LatestPageRollupBucket"] = 1
	}

	dias, err := q.ListPublicDailyUptime(ctx, db.ListPublicDailyUptimeParams{PageID: pagina, Since: desde, Cut: corte, Until: ate})
	if err != nil {
		t.Fatal(err)
	}
	n["ListPublicDailyUptime"] = len(dias)

	latencias, err := q.ListPublicLatency(ctx, db.ListPublicLatencyParams{PageID: pagina, Since: desde, Cut: corte, Until: ate})
	if err != nil {
		t.Fatal(err)
	}
	n["ListPublicLatency"] = len(latencias)

	incidentes, err := q.ListPublicIncidents(ctx, db.ListPublicIncidentsParams{PageID: pagina, Since: desde, MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	n["ListPublicIncidents"] = len(incidentes)
	return n
}

var consultasPublicas = []string{
	"ListPublicItems", "LatestPageRollupBucket", "ListPublicDailyUptime", "ListPublicLatency", "ListPublicIncidents",
}

func TestPublicReadsSeeThePublishedPageWithoutRLS(t *testing.T) {
	q, pagina := paginaComDados(t, true, "publicada")
	n := leiturasPublicas(t, q, pagina)
	for _, c := range consultasPublicas {
		if n[c] == 0 {
			t.Errorf("%s não devolveu nada da página publicada: o controle não prova o filtro", c)
		}
	}
}

func TestPublicReadsFilterPublishedWithoutRLS(t *testing.T) {
	q, pagina := paginaComDados(t, false, "rascunho")
	n := leiturasPublicas(t, q, pagina)
	for _, c := range consultasPublicas {
		if n[c] != 0 {
			t.Errorf("%s devolveu %d linhas do rascunho sem RLS, queria 0", c, n[c])
		}
	}
}
