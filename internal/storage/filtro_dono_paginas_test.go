package storage_test

import (
	"context"
	"slices"
	"testing"
	"time"

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

// mesmaPagina compara campo a campo, com CreatedAt por Equal: a documentação de
// time desaconselha == entre instantes, que também compara Location e relógio
// monotônico.
func mesmaPagina(x, y db.StatusPage) bool {
	criadasJuntas := x.CreatedAt.Equal(y.CreatedAt)
	x.CreatedAt, y.CreatedAt = time.Time{}, time.Time{}
	return criadasJuntas && x == y
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
	paginaDeA := func() db.StatusPage {
		t.Helper()
		p, err := q.GetStatusPage(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	original := paginaDeA()

	if n := atualizar(b, "de-b"); n != 0 {
		t.Errorf("B alterou %d páginas de A sem RLS, queria 0", n)
	}
	if p := paginaDeA(); !mesmaPagina(p, original) {
		t.Errorf("depois do UPDATE de B sem RLS, a página de A ficou %+v, queria %+v", p, original)
	}
	n := atualizar(a, "nova-a")
	if p := paginaDeA(); n != 1 || p.Slug != "nova-a" || p.Title != "Outro" || p.Published {
		t.Errorf("A alterou a própria página: %d linhas, %+v; queria 1 com nova-a, Outro e não publicada", n, p)
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
