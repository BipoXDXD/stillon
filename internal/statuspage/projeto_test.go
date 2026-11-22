package statuspage_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/statuspage"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// escopoNo é o escopo de usuario no projeto com o papel dado, montado à mão:
// o serviço tem de conferir o papel no banco, não confiar neste.
func escopoNo(projeto, usuario uuid.UUID, papel tenancy.Role) tenancy.ProjectScope {
	return tenancy.ProjectScope{ProjectID: projeto, UserID: usuario, Role: papel}
}

// escopo é o escopo de usuario no Projeto padrão dele, criado na primeira
// chamada (como faz a rota antiga). Sai de storage.InProjectTx, com o papel
// lido do banco, e não montado à mão.
func (a ambiente) escopo(t *testing.T, usuario uuid.UUID) tenancy.ProjectScope {
	t.Helper()
	projeto := testdb.ProjetoPadrao(t, usuario)
	var sc tenancy.ProjectScope
	err := storage.InProjectTx(context.Background(), a.pool, usuario, projeto, func(_ *db.Queries, lido tenancy.ProjectScope) error {
		sc = lido
		return nil
	})
	if err != nil {
		t.Fatalf("escopo de %s no Projeto padrão: %v", usuario, err)
	}
	return sc
}

// alvoNoProjeto cria um alvo do dono no projeto dado, pelo escopo de projeto.
func (a ambiente) alvoNoProjeto(t *testing.T, dono, projeto uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProjectTx(ctx, a.pool, dono, projeto, func(q *db.Queries, _ tenancy.ProjectScope) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, ProjectID: projeto, Name: nome, URL: "https://interno.exemplo.com/health",
			Method: "GET", IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true,
			NextRunAt: a.clk.Now(), CreatedAt: a.clk.Now(),
		})
	})
	if err != nil {
		t.Fatalf("criar alvo no projeto %s: %v", projeto, err)
	}
	return id
}

// itensNoBanco lê, como superusuário, os alvos dos itens da página.
func itensNoBanco(t *testing.T, pagina uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := testdb.Admin(t).Query(context.Background(),
		`SELECT target_id FROM status_page_items WHERE status_page_id = $1 ORDER BY position`, pagina)
	if err != nil {
		t.Fatalf("ler os itens da página %s como superusuário: %v", pagina, err)
	}
	defer rows.Close()
	var alvos []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		alvos = append(alvos, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return alvos
}

// Dois projetos do MESMO usuário: o alvo de B não entra na página de A. A
// recusa é o erro de validação do item, e nenhuma linha é gravada.
func TestStatusPageRefusesTargetOfOtherProject(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono := a.usuario(t)
	projetoA := testdb.ProjetoPadrao(t, dono)
	projetoB := testdb.NovoProjeto(t, dono, "B")
	emA := escopoNo(projetoA, dono, tenancy.RoleOwner)
	alvoA := a.alvoNoProjeto(t, dono, projetoA, "api")
	alvoB := a.alvoNoProjeto(t, dono, projetoB, "de-b")
	p, err := a.svc.Create(ctx, emA, statuspage.Input{
		Slug: "empresa", Title: "Status", Items: []statuspage.Item{{TargetID: alvoA, DisplayName: "API"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	itens := []statuspage.Item{{TargetID: alvoB, DisplayName: "De B"}}
	_, err = a.svc.Update(ctx, emA, p.ID, statuspage.Patch{Items: &itens})
	quer := []statuspage.FieldError{{Field: "items[0].target_id", Reason: statuspage.ReasonInvalid}}
	if got := campos(err); !slices.Equal(got, quer) {
		t.Fatalf("Update com o alvo de B no escopo de A: campos %v (erro %v), queria %v", got, err, quer)
	}
	if got := itensNoBanco(t, p.ID); !slices.Equal(got, []uuid.UUID{alvoA}) {
		t.Errorf("os itens da página ficaram %v, queria só %s (o alvo de B não pode entrar)", got, alvoA)
	}
}

// O member não cria nem edita a status page. O escopo traz owner de
// propósito: o papel que vale é o lido na transação, e no banco ele é member.
func TestMemberCannotEditStatusPage(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, member := a.usuario(t), a.usuario(t)
	projeto := testdb.ProjetoPadrao(t, dono)
	testdb.Membro(t, projeto, member, "member")
	p, err := a.svc.Create(ctx, escopoNo(projeto, dono, tenancy.RoleOwner), statuspage.Input{Slug: "empresa", Title: "Status"})
	if err != nil {
		t.Fatal(err)
	}
	comoMember := escopoNo(projeto, member, tenancy.RoleOwner)

	if _, err := a.svc.Create(ctx, comoMember, statuspage.Input{Slug: "do-member", Title: "Do member"}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Create como member: erro %v, queria ErrForbidden", err)
	}
	titulo, publicada := "Trocado pelo member", true
	if _, err := a.svc.Update(ctx, comoMember, p.ID, statuspage.Patch{Title: &titulo, Published: &publicada}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Update como member: erro %v, queria ErrForbidden", err)
	}
	// O member lê (PermRead): não é ErrForbidden. A política permissiva ainda
	// é por dono, então hoje ele não vê a página que o titular criou.
	// TODO(PR 2b): passa a ver a página do titular quando as permissivas forem por projeto
	if _, err := a.svc.Get(ctx, comoMember); !errors.Is(err, statuspage.ErrNotFound) {
		t.Errorf("Get como member: erro %v, queria ErrNotFound (lê, mas a permissiva ainda é por dono)", err)
	}

	var n int
	var tituloNoBanco string
	var publicadaNoBanco bool
	if err := testdb.Admin(t).QueryRow(ctx,
		`SELECT (SELECT count(*) FROM status_pages WHERE project_id = $1), title, published FROM status_pages WHERE id = $2`,
		projeto, p.ID).Scan(&n, &tituloNoBanco, &publicadaNoBanco); err != nil {
		t.Fatal(err)
	}
	if n != 1 || tituloNoBanco != "Status" || publicadaNoBanco {
		t.Errorf("o projeto ficou com %d páginas, a do dono com título %q e publicada %v; queria 1, %q e rascunho",
			n, tituloNoBanco, publicadaNoBanco, "Status")
	}
}

// A segunda página do mesmo projeto é ErrExists, também quando quem a cria
// ainda não tem página (o manager): aí quem dispara é o UNIQUE de project_id,
// e não o de user_id. Sem o mapeamento, viraria erro interno (500).
func TestSecondPageInSameProjectIsExists(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	dono, manager := a.usuario(t), a.usuario(t)
	projeto := testdb.ProjetoPadrao(t, dono)
	testdb.Membro(t, projeto, manager, "manager")
	if _, err := a.svc.Create(ctx, escopoNo(projeto, dono, tenancy.RoleOwner), statuspage.Input{Slug: "empresa", Title: "Status"}); err != nil {
		t.Fatal(err)
	}

	_, err := a.svc.Create(ctx, escopoNo(projeto, manager, tenancy.RoleManager), statuspage.Input{Slug: "outra", Title: "Outra"})
	if !errors.Is(err, statuspage.ErrExists) {
		t.Errorf("segunda página no mesmo projeto, pelo manager: erro %v, queria ErrExists", err)
	}
}
