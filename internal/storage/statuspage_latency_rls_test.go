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

// latenciaPublica roda ListPublicLatency no escopo público de slug, para a
// página pageID, e devolve a latência por alvo.
func latenciaPublica(t *testing.T, pool *pgxpool.Pool, slug string, pageID uuid.UUID) map[uuid.UUID]int32 {
	t.Helper()
	ctx := context.Background()
	got := map[uuid.UUID]int32{}
	err := storage.InPublicTx(ctx, pool, slug, func(q *db.Queries) error {
		linhas, err := q.ListPublicLatency(ctx, db.ListPublicLatencyParams{
			PageID: pageID, Since: agora.Add(-24 * time.Hour), Cut: agora.Add(-time.Hour), Until: agora.Add(time.Hour),
		})
		for _, l := range linhas {
			got[l.TargetID] = l.P50
		}
		return err
	})
	if err != nil {
		t.Fatalf("ListPublicLatency(slug %q): %v", slug, err)
	}
	return got
}

// ListPublicLatency filtra só pelo page_id que recebe, sem olhar se a página
// está publicada nem se é a do slug. Quem segura isso é a RLS do escopo
// público: o page_id de um rascunho ou da página de outro dono não devolve
// latência nenhuma, mesmo com checks gravados para os alvos dela. Cada dono
// tem uma página só, por isso o rascunho é de um terceiro usuário.
func TestPublicLatencyOnlyForThePublishedPageOfTheSlug(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b, c := novoUsuario(t, pool), novoUsuario(t, pool), novoUsuario(t, pool)
	naPagina, foraDaPagina := novoAlvo(t, pool, a, agora), novoAlvo(t, pool, a, agora)
	noRascunho := novoAlvo(t, pool, c, agora)
	alvoB := novoAlvo(t, pool, b, agora)
	paginaA := novaPagina(t, pool, a, "pagina-a", true, naPagina)
	rascunho := novaPagina(t, pool, c, "rascunho-c", false, noRascunho)
	paginaB := novaPagina(t, pool, b, "pagina-b", true, alvoB)
	if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
		t.Fatal(err)
	}
	for _, alvo := range []uuid.UUID{naPagina, foraDaPagina, noRascunho, alvoB} {
		novoCheck(t, pool, alvo, agora) // latência 12 ms
	}
	// Sem os checks dos outros alvos, o vazio abaixo seria verdade por acidente.
	if n := contarComoSondador(t, pool, `SELECT count(*) FROM checks`); n != 4 {
		t.Fatalf("a preparação gravou %d checks, queria 4", n)
	}

	got := latenciaPublica(t, pool, "pagina-a", paginaA)
	if len(got) != 1 || got[naPagina] != 12 {
		t.Errorf("pagina-a com o próprio id: %v, queria só o alvo da página com 12 ms", got)
	}
	if got := latenciaPublica(t, pool, "pagina-b", paginaB); len(got) != 1 || got[alvoB] != 12 {
		t.Errorf("pagina-b com o próprio id: %v, queria só o alvo de B com 12 ms (controle)", got)
	}

	for _, caso := range []struct {
		nome   string
		slug   string
		pageID uuid.UUID
	}{
		{"slug de A com o id da página de B", "pagina-a", paginaB},
		{"slug de A com o id do rascunho de C", "pagina-a", rascunho},
		{"slug do rascunho com o id do rascunho", "rascunho-c", rascunho},
		{"slug inexistente com o id da página de A", "nao-existe", paginaA},
	} {
		if got := latenciaPublica(t, pool, caso.slug, caso.pageID); len(got) != 0 {
			t.Errorf("%s: devolveu %v, queria nenhuma latência", caso.nome, got)
		}
	}
}
