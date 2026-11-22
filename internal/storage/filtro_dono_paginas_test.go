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

// Espelho de filtro_dono_test.go para status_pages e status_page_items:
// superusuário, sem RLS, e só o project_id do WHERE separa A de B. A e B são
// dois projetos do MESMO usuário: o filtro por dono os deixaria passar.

// itensDaPagina lê os alvos da página no projeto dado, sem RLS.
func itensDaPagina(t *testing.T, q *db.Queries, projeto, pagina uuid.UUID) []uuid.UUID {
	t.Helper()
	itens, err := q.ListStatusPageItems(context.Background(), db.ListStatusPageItemsParams{ProjectID: projeto, PageID: pagina})
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
// monotônico. ProjectID é ponteiro (a coluna ainda aceita nulo): compara o
// valor, não o endereço.
func mesmaPagina(x, y db.StatusPage) bool {
	criadasJuntas := x.CreatedAt.Equal(y.CreatedAt)
	mesmoProjeto := (x.ProjectID == nil) == (y.ProjectID == nil) &&
		(x.ProjectID == nil || *x.ProjectID == *y.ProjectID)
	x.CreatedAt, y.CreatedAt = time.Time{}, time.Time{}
	x.ProjectID, y.ProjectID = nil, nil
	return criadasJuntas && mesmoProjeto && x == y
}

func TestUpdateStatusPageFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	paginaA := novaPagina(t, pool, a, "pagina-a", true)
	q := queriesSemRLS(t)
	atualizar := func(projeto uuid.UUID, slug string) int64 {
		t.Helper()
		n, err := q.UpdateStatusPage(ctx, db.UpdateStatusPageParams{
			ProjectID: projeto, ID: paginaA, Slug: slug, Title: "Outro", Published: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	paginaDeA := func() db.StatusPage {
		t.Helper()
		p, err := q.GetStatusPage(ctx, pa)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	original := paginaDeA()

	if n := atualizar(pb, "de-b"); n != 0 {
		t.Errorf("B alterou %d páginas de A sem RLS, queria 0", n)
	}
	if p := paginaDeA(); !mesmaPagina(p, original) {
		t.Errorf("depois do UPDATE de B sem RLS, a página de A ficou %+v, queria %+v", p, original)
	}
	n := atualizar(pa, "nova-a")
	if p := paginaDeA(); n != 1 || p.Slug != "nova-a" || p.Title != "Outro" || p.Published {
		t.Errorf("A alterou a própria página: %d linhas, %+v; queria 1 com nova-a, Outro e não publicada", n, p)
	}
}

func TestListStatusPageItemsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	paginaA := novaPagina(t, pool, a, "pagina-a", true, alvoA)
	q := queriesSemRLS(t)

	if itens := itensDaPagina(t, q, pb, paginaA); len(itens) != 0 {
		t.Errorf("B leu os itens %v da página de A sem RLS", itens)
	}
	if itens := itensDaPagina(t, q, pa, paginaA); !slices.Equal(itens, []uuid.UUID{alvoA}) {
		t.Errorf("A leu %v, queria só %s", itens, alvoA)
	}
}

func TestDeleteStatusPageItemsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	paginaA := novaPagina(t, pool, a, "pagina-a", true, alvoA)
	q := queriesSemRLS(t)

	if err := q.DeleteStatusPageItems(ctx, db.DeleteStatusPageItemsParams{ProjectID: pb, PageID: paginaA}); err != nil {
		t.Fatal(err)
	}
	if itens := itensDaPagina(t, q, pa, paginaA); !slices.Equal(itens, []uuid.UUID{alvoA}) {
		t.Errorf("depois do DELETE de B sem RLS, a página de A ficou com %v", itens)
	}
	if err := q.DeleteStatusPageItems(ctx, db.DeleteStatusPageItemsParams{ProjectID: pa, PageID: paginaA}); err != nil {
		t.Fatal(err)
	}
	if itens := itensDaPagina(t, q, pa, paginaA); len(itens) != 0 {
		t.Errorf("A apagou os próprios itens e sobrou %v", itens)
	}
}

func TestOwnTargetIDsFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	alvoA := novoAlvo(t, pool, a, agora)
	q := queriesSemRLS(t)

	deB, err := q.OwnTargetIDs(ctx, db.OwnTargetIDsParams{ProjectID: pb, Ids: []uuid.UUID{alvoA}})
	if err != nil {
		t.Fatal(err)
	}
	if len(deB) != 0 {
		t.Errorf("B tomou o alvo de A como seu sem RLS: %v", deB)
	}
	deA, err := q.OwnTargetIDs(ctx, db.OwnTargetIDsParams{ProjectID: pa, Ids: []uuid.UUID{alvoA}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deA, []uuid.UUID{alvoA}) {
		t.Errorf("A recebeu %v, queria só %s", deA, alvoA)
	}
}

func TestGetStatusPageFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	paginaA := novaPagina(t, pool, a, "pagina-a", true)
	q := queriesSemRLS(t)

	_, err := q.GetStatusPage(ctx, pb)
	exigirNenhumaLinha(t, err, "GetStatusPage")
	p, err := q.GetStatusPage(ctx, pa)
	if err != nil {
		t.Fatalf("A lendo a própria página: %v", err)
	}
	if p.ID != paginaA || p.UserID != a {
		t.Errorf("A leu a página %s do dono %s, queria %s de %s", p.ID, p.UserID, paginaA, a)
	}
}

func TestLockStatusPageFiltersOwnerWithoutRLS(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := novoUsuario(t, pool)
	pa, pb := testdb.ProjetoPadrao(t, a), testdb.NovoProjeto(t, a, "B")
	paginaA := novaPagina(t, pool, a, "pagina-a", true)
	q := queriesSemRLS(t)

	_, err := q.LockStatusPage(ctx, pb)
	exigirNenhumaLinha(t, err, "LockStatusPage")
	id, err := q.LockStatusPage(ctx, pa)
	if err != nil {
		t.Fatalf("A travando a própria página: %v", err)
	}
	if id != paginaA {
		t.Errorf("A travou a página %s, queria %s", id, paginaA)
	}
}
