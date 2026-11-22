package targets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// escopoNo é o escopo de usuario no projeto com o papel dado, montado à mão:
// o serviço tem de conferir o papel no banco, não confiar neste.
func escopoNo(projeto, usuario uuid.UUID, papel tenancy.Role) tenancy.ProjectScope {
	return tenancy.ProjectScope{ProjectID: projeto, UserID: usuario, Role: papel}
}

// alvoNoBanco lê, como superusuário, o nome, o projeto e o criador do alvo.
func (a ambiente) alvoNoBanco(t *testing.T, id uuid.UUID) (nome string, projeto, criador uuid.UUID) {
	t.Helper()
	if err := a.admin.QueryRow(context.Background(),
		`SELECT name, project_id, user_id FROM targets WHERE id = $1`, id).Scan(&nome, &projeto, &criador); err != nil {
		t.Fatalf("ler o alvo %s como superusuário: %v", id, err)
	}
	return nome, projeto, criador
}

func (a ambiente) alvosDoProjeto(t *testing.T, projeto uuid.UUID) int {
	t.Helper()
	var n int
	if err := a.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM targets WHERE project_id = $1`, projeto).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Dois projetos do MESMO usuário: o alvo de B não existe no escopo de A, nem
// pela leitura nem pela escrita.
func TestTargetsOfOtherProjectOfSameUserAreNotFound(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	u := a.usuario(t)
	projetoA := testdb.ProjetoPadrao(t, u)
	projetoB := testdb.NovoProjeto(t, u, "B")
	emA, emB := escopoNo(projetoA, u, tenancy.RoleOwner), escopoNo(projetoB, u, tenancy.RoleOwner)
	alvoB, err := a.svc.Create(ctx, emB, entrada())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.svc.Get(ctx, emA, alvoB.ID); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Get no escopo de A: erro %v, queria ErrNotFound", err)
	}
	novo := "renomeado por A"
	if _, err := a.svc.Update(ctx, emA, alvoB.ID, targets.Patch{Name: &novo}); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Update no escopo de A: erro %v, queria ErrNotFound", err)
	}
	if err := a.svc.Delete(ctx, emA, alvoB.ID); !errors.Is(err, targets.ErrNotFound) {
		t.Errorf("Delete no escopo de A: erro %v, queria ErrNotFound", err)
	}
	lista, err := a.svc.List(ctx, emA, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, alvo := range lista {
		if alvo.ID == alvoB.ID {
			t.Errorf("List no escopo de A trouxe o alvo %s de B", alvoB.ID)
		}
	}
	if nome, projeto, _ := a.alvoNoBanco(t, alvoB.ID); nome != entrada().Name || projeto != projetoB {
		t.Errorf("o alvo de B ficou com nome %q no projeto %s, queria %q em %s", nome, projeto, entrada().Name, projetoB)
	}
}

// O viewer lê o projeto, mas não cria, não edita e não apaga: o banco fica
// como estava.
func TestViewerReadsButCannotChangeTargets(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	titular, v := a.usuario(t), a.usuario(t)
	projeto := testdb.ProjetoPadrao(t, titular)
	testdb.Membro(t, projeto, v, "viewer")
	alvo, err := a.svc.Create(ctx, escopoNo(projeto, titular, tenancy.RoleOwner), entrada())
	if err != nil {
		t.Fatal(err)
	}
	comoViewer := escopoNo(projeto, v, tenancy.RoleViewer)

	// O viewer lê (PermRead), mas a política permissiva ainda é por dono:
	// hoje ele não vê o alvo que o titular criou.
	// TODO(PR 2b): passa a ver os alvos do titular quando as permissivas forem por projeto
	vistos, err := a.svc.List(ctx, comoViewer, nil, 10)
	if err != nil {
		t.Errorf("List como viewer: %v, queria sem erro", err)
	}
	if len(vistos) != 0 {
		t.Errorf("List como viewer trouxe %d alvos, queria 0 enquanto a permissiva for por dono", len(vistos))
	}
	if _, err := a.svc.Create(ctx, comoViewer, entrada()); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Create como viewer: erro %v, queria ErrForbidden", err)
	}
	novo := "renomeado pelo viewer"
	if _, err := a.svc.Update(ctx, comoViewer, alvo.ID, targets.Patch{Name: &novo}); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Update como viewer: erro %v, queria ErrForbidden", err)
	}
	if err := a.svc.Delete(ctx, comoViewer, alvo.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Delete como viewer: erro %v, queria ErrForbidden", err)
	}

	if n := a.alvosDoProjeto(t, projeto); n != 1 {
		t.Errorf("o projeto ficou com %d alvos, queria 1", n)
	}
	if nome, _, _ := a.alvoNoBanco(t, alvo.ID); nome != entrada().Name {
		t.Errorf("o alvo ficou com nome %q, queria %q", nome, entrada().Name)
	}
}

// O papel que vale é o lido na transação: um escopo que diz owner não faz de
// um viewer um editor.
func TestTargetRoleIsReadInsideTheTransaction(t *testing.T) {
	a := novoAmbiente(t)
	titular, v := a.usuario(t), a.usuario(t)
	projeto := testdb.ProjetoPadrao(t, titular)
	testdb.Membro(t, projeto, v, "viewer")

	_, err := a.svc.Create(context.Background(), escopoNo(projeto, v, tenancy.RoleOwner), entrada())

	if !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Create com escopo owner e papel viewer no banco: erro %v, queria ErrForbidden", err)
	}
	if n := a.alvosDoProjeto(t, projeto); n != 0 {
		t.Errorf("o projeto ficou com %d alvos, queria nenhum", n)
	}
}

// O alvo nasce no projeto do escopo, e user_id guarda quem o criou: aqui um
// member, não o titular.
func TestCreateTargetRecordsProjectAndCreator(t *testing.T) {
	a := novoAmbiente(t)
	titular, u := a.usuario(t), a.usuario(t)
	projeto := testdb.NovoProjeto(t, titular, "A")
	testdb.Membro(t, projeto, u, "member")

	alvo, err := a.svc.Create(context.Background(), escopoNo(projeto, u, tenancy.RoleMember), entrada())
	if err != nil {
		t.Fatal(err)
	}

	if _, gravadoEm, criador := a.alvoNoBanco(t, alvo.ID); gravadoEm != projeto || criador != u {
		t.Errorf("alvo gravado no projeto %s com user_id %s, queria %s e %s", gravadoEm, criador, projeto, u)
	}
}
