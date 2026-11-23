package history_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/incident/history"
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

// alvoNoProjeto grava um alvo de dono no projeto dado, sob o escopo de
// projeto (o gatilho só preencheria o Projeto padrão).
func (b *bancada) alvoNoProjeto(t *testing.T, dono, projeto uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	err := storage.InProjectTx(ctx, b.pool, dono, projeto, func(q *db.Queries, _ tenancy.ProjectScope) error {
		return q.InsertTarget(ctx, db.InsertTargetParams{
			ID: id, UserID: dono, ProjectID: projeto, Name: nome, URL: "https://exemplo.com", Method: "GET",
			IntervalSec: 60, TimeoutMS: 5000, ExpectedStatus: 200, Enabled: true, NextRunAt: agora, CreatedAt: agora,
		})
	})
	if err != nil {
		t.Fatalf("criar o alvo %q no projeto %s: %v", nome, projeto, err)
	}
	return id
}

// ackNoBanco lê ack_at como superusuário, por fora das queries testadas.
func ackNoBanco(t *testing.T, incidente uuid.UUID) *time.Time {
	t.Helper()
	var ack *time.Time
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT ack_at FROM incidents WHERE id = $1`, incidente).Scan(&ack); err != nil {
		t.Fatalf("ler o ack do incidente %s como superusuário: %v", incidente, err)
	}
	return ack
}

// Dois projetos do MESMO usuário: o incidente de B não aparece na lista de A,
// nem com o filtro pelo alvo de B, e não é reconhecido pelo escopo de A.
func TestIncidentsOfOtherProjectOfSameUserAreHidden(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono := b.usuario(t)
	projetoA := testdb.ProjetoPadrao(t, dono)
	projetoB := testdb.NovoProjeto(t, dono, "B")
	emA, emB := escopoNo(projetoA, dono, tenancy.RoleOwner), escopoNo(projetoB, dono, tenancy.RoleOwner)
	deA := b.incidente(t, b.alvoNoProjeto(t, dono, projetoA, "de A"), false)
	alvoB := b.alvoNoProjeto(t, dono, projetoB, "de B")
	deB := b.incidente(t, alvoB, false)

	if got, err := b.svc.List(ctx, emA, history.Filter{}); err != nil || len(got) != 1 || got[0].ID != deA {
		t.Errorf("List no escopo de A = %v, %v; queria só o incidente %s", ids(got), err, deA)
	}
	if got, err := b.svc.List(ctx, emA, history.Filter{TargetID: &alvoB}); err != nil || len(got) != 0 {
		t.Errorf("List no escopo de A pelo alvo de B = %v, %v; queria vazio e sem erro", ids(got), err)
	}
	if _, err := b.svc.Ack(ctx, emA, deB); !errors.Is(err, history.ErrNotFound) {
		t.Errorf("Ack no escopo de A do incidente de B: erro %v, queria ErrNotFound", err)
	}
	if ack := ackNoBanco(t, deB); ack != nil {
		t.Errorf("o incidente de B ficou com ack_at %s depois do ack no escopo de A, queria nulo", ack)
	}
	if got, err := b.svc.List(ctx, emB, history.Filter{}); err != nil || len(got) != 1 || got[0].ID != deB {
		t.Errorf("List no escopo de B = %v, %v; queria só o incidente %s", ids(got), err, deB)
	}
}

// O viewer lê, mas não reconhece. O escopo traz owner de propósito: o papel
// que vale é o lido na transação, e no banco ele é viewer.
func TestViewerCannotAckIncident(t *testing.T) {
	b := novaBancada(t)
	ctx := context.Background()
	dono, leitor := b.usuario(t), b.usuario(t)
	projeto := testdb.ProjetoPadrao(t, dono)
	testdb.Membro(t, projeto, leitor, "viewer")
	incidente := b.incidente(t, b.alvoNoProjeto(t, dono, projeto, "API"), false)
	comoViewer := escopoNo(projeto, leitor, tenancy.RoleOwner)

	if _, err := b.svc.Ack(ctx, comoViewer, incidente); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Ack como viewer: erro %v, queria ErrForbidden", err)
	}
	if ack := ackNoBanco(t, incidente); ack != nil {
		t.Errorf("o ack do viewer gravou ack_at %s, queria nulo", ack)
	}
	// O viewer lê (PermRead) o incidente do alvo que o titular criou: a
	// política é por projeto.
	lista, err := b.svc.List(ctx, comoViewer, history.Filter{})
	if err != nil {
		t.Errorf("List como viewer: erro %v, queria sem erro (viewer lê)", err)
	}
	if !slices.ContainsFunc(lista, func(i history.Incident) bool { return i.ID == incidente }) {
		t.Errorf("List como viewer trouxe %+v, queria o incidente %s", lista, incidente)
	}
}
