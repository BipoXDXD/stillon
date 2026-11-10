package storage_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Espelho de filtro_dono_test.go para status_pages e status_page_items.

// itensDaPagina lê os alvos da página como o dono, sem RLS.
func itensDaPagina(t *testing.T, q *db.Queries, dono, pagina uuid.UUID) []uuid.UUID {
	t.Helper()
	itens, err := q.ListStatusPageItems(context.Background(), db.ListStatusPageItemsParams{UserID: dono, PageID: pagina})
	if err != nil {
		t.Fatal(err)
	}
	alvos := make([]uuid.UUID, 0, len(itens))
	for _, i := range itens {
		alvos = append(alvos, i.TargetID)
	}
	return alvos
}

func TestUpdateStatusPageFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	paginaA := novaPagina(t, pool, a, "pagina-a", true)
	q := queriesSemRLS(t)
	atualizar := func(dono uuid.UUID, slug string) int64 {
		t.Helper()
		n, err := q.UpdateStatusPage(ctx, db.UpdateStatusPageParams{
			UserID: dono, ID: paginaA, Slug: slug, Title: "Outro", Published: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	slugDeA := func() string {
		t.Helper()
		p, err := q.GetStatusPage(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return p.Slug
	}

	if n := atualizar(b, "de-b"); n != 0 || slugDeA() != "pagina-a" {
		t.Errorf("B alterou a página de A sem RLS: %d linhas, slug %q", n, slugDeA())
	}
	if n := atualizar(a, "nova-a"); n != 1 || slugDeA() != "nova-a" {
		t.Errorf("A alterou a própria página: %d linhas, slug %q; queria 1 e nova-a", n, slugDeA())
	}
}

func TestListStatusPageItemsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	paginaA := novaPagina(t, pool, a, "pagina-a", true, alvoA)
	q := queriesSemRLS(t)

	if itens := itensDaPagina(t, q, b, paginaA); len(itens) != 0 {
		t.Errorf("B leu os itens %v da página de A sem RLS", itens)
	}
	if itens := itensDaPagina(t, q, a, paginaA); !slices.Equal(itens, []uuid.UUID{alvoA}) {
		t.Errorf("A leu %v, queria só %s", itens, alvoA)
	}
}

func TestDeleteStatusPageItemsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	paginaA := novaPagina(t, pool, a, "pagina-a", true, alvoA)
	q := queriesSemRLS(t)

	if err := q.DeleteStatusPageItems(ctx, db.DeleteStatusPageItemsParams{UserID: b, PageID: paginaA}); err != nil {
		t.Fatal(err)
	}
	if itens := itensDaPagina(t, q, a, paginaA); !slices.Equal(itens, []uuid.UUID{alvoA}) {
		t.Errorf("depois do DELETE de B sem RLS, a página de A ficou com %v", itens)
	}
	if err := q.DeleteStatusPageItems(ctx, db.DeleteStatusPageItemsParams{UserID: a, PageID: paginaA}); err != nil {
		t.Fatal(err)
	}
	if itens := itensDaPagina(t, q, a, paginaA); len(itens) != 0 {
		t.Errorf("A apagou os próprios itens e sobrou %v", itens)
	}
}

func TestOwnTargetIDsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)

	deB, err := q.OwnTargetIDs(ctx, db.OwnTargetIDsParams{UserID: b, Ids: []uuid.UUID{alvoA}})
	if err != nil {
		t.Fatal(err)
	}
	if len(deB) != 0 {
		t.Errorf("B tomou o alvo de A como seu sem RLS: %v", deB)
	}
	deA, err := q.OwnTargetIDs(ctx, db.OwnTargetIDsParams{UserID: a, Ids: []uuid.UUID{alvoA}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deA, []uuid.UUID{alvoA}) {
		t.Errorf("A recebeu %v, queria só %s", deA, alvoA)
	}
}
