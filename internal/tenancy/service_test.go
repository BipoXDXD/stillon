package tenancy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// repoFalso guarda o que EnsureDefault, Create, ChangeRole e Rename receberam.
type repoFalso struct {
	nowRecebido   time.Time
	escopo        tenancy.ProjectScope
	novoRecebido  tenancy.NewProject
	papelRecebido tenancy.Role
	nomeRecebido  string
}

func (r *repoFalso) ListMembers(context.Context, uuid.UUID, uuid.UUID) ([]tenancy.MemberView, error) {
	return nil, nil
}

func (r *repoFalso) ChangeRole(_ context.Context, _, _, alvo uuid.UUID, para tenancy.Role) (tenancy.MemberView, error) {
	r.papelRecebido = para
	return tenancy.MemberView{UserID: alvo, Role: para}, nil
}

func (r *repoFalso) RemoveMember(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }

func (r *repoFalso) ProposeTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (r *repoFalso) AcceptTransfer(context.Context, uuid.UUID, uuid.UUID) (tenancy.ProjectSummary, error) {
	return tenancy.ProjectSummary{}, nil
}

func (r *repoFalso) CancelTransfer(context.Context, uuid.UUID, uuid.UUID) error { return nil }

func (r *repoFalso) Rename(_ context.Context, _, projeto uuid.UUID, nome string) (tenancy.ProjectSummary, error) {
	r.nomeRecebido = nome
	return tenancy.ProjectSummary{ID: projeto, Name: nome}, nil
}

func (r *repoFalso) Membership(context.Context, uuid.UUID, uuid.UUID) (tenancy.ProjectScope, error) {
	return r.escopo, nil
}

func (r *repoFalso) EnsureDefault(_ context.Context, _ uuid.UUID, now time.Time) (tenancy.ProjectScope, error) {
	r.nowRecebido = now
	return r.escopo, nil
}

func (r *repoFalso) ListForUser(context.Context, uuid.UUID) ([]tenancy.ProjectSummary, error) {
	return nil, nil
}

func (r *repoFalso) Create(_ context.Context, p tenancy.NewProject) (tenancy.ProjectSummary, error) {
	r.novoRecebido = p
	return tenancy.ProjectSummary{ID: p.ID, Name: p.Name, Role: tenancy.RoleOwner, CreatedAt: p.Now}, nil
}

func TestServiceDefaultScopeUsesClock(t *testing.T) {
	agora := time.Date(2026, 11, 22, 10, 30, 0, 0, time.UTC)
	u, p := uuid.New(), uuid.New()
	repo := &repoFalso{escopo: tenancy.ProjectScope{ProjectID: p, UserID: u, Role: tenancy.RoleOwner}}
	svc := tenancy.NewService(repo, clock.NewFake(agora))

	sc, err := svc.DefaultScope(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.nowRecebido.Equal(agora) {
		t.Errorf("now repassado = %v, queria %v (clk.Now())", repo.nowRecebido, agora)
	}
	if sc != repo.escopo {
		t.Errorf("escopo = %+v, queria %+v", sc, repo.escopo)
	}
}

// repoQueFalha reprova o teste se o Service chegar ao repositório.
type repoQueFalha struct{ t *testing.T }

func (r repoQueFalha) Membership(context.Context, uuid.UUID, uuid.UUID) (tenancy.ProjectScope, error) {
	r.t.Error("Membership chamado com projeto nulo")
	return tenancy.ProjectScope{}, nil
}

func (r repoQueFalha) EnsureDefault(context.Context, uuid.UUID, time.Time) (tenancy.ProjectScope, error) {
	r.t.Error("EnsureDefault chamado")
	return tenancy.ProjectScope{}, nil
}

func (r repoQueFalha) ListForUser(context.Context, uuid.UUID) ([]tenancy.ProjectSummary, error) {
	r.t.Error("ListForUser chamado")
	return nil, nil
}

func (r repoQueFalha) Create(context.Context, tenancy.NewProject) (tenancy.ProjectSummary, error) {
	r.t.Error("Create chamado")
	return tenancy.ProjectSummary{}, nil
}

func (r repoQueFalha) ListMembers(context.Context, uuid.UUID, uuid.UUID) ([]tenancy.MemberView, error) {
	r.t.Error("ListMembers chamado")
	return nil, nil
}

func (r repoQueFalha) ChangeRole(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, tenancy.Role) (tenancy.MemberView, error) {
	r.t.Error("ChangeRole chamado")
	return tenancy.MemberView{}, nil
}

func (r repoQueFalha) RemoveMember(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	r.t.Error("RemoveMember chamado")
	return nil
}

func (r repoQueFalha) ProposeTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	r.t.Error("ProposeTransfer chamado")
	return nil
}

func (r repoQueFalha) AcceptTransfer(context.Context, uuid.UUID, uuid.UUID) (tenancy.ProjectSummary, error) {
	r.t.Error("AcceptTransfer chamado")
	return tenancy.ProjectSummary{}, nil
}

func (r repoQueFalha) CancelTransfer(context.Context, uuid.UUID, uuid.UUID) error {
	r.t.Error("CancelTransfer chamado")
	return nil
}

func (r repoQueFalha) Rename(context.Context, uuid.UUID, uuid.UUID, string) (tenancy.ProjectSummary, error) {
	r.t.Error("Rename chamado")
	return tenancy.ProjectSummary{}, nil
}

func TestScopeRefusesNilProjectWithoutRepository(t *testing.T) {
	svc := tenancy.NewService(repoQueFalha{t: t}, clock.NewFake(time.Now()))

	_, err := svc.Scope(context.Background(), uuid.New(), uuid.Nil)
	if !errors.Is(err, tenancy.ErrProjectNotFound) {
		t.Errorf("err = %v, queria ErrProjectNotFound", err)
	}
}

// O nome é validado e normalizado antes do repositório: só espaço não chega
// lá, e o nome aparado vai com o titular, um id novo e o relógio do Service.
func TestServiceCreateValidatesNameBeforeRepository(t *testing.T) {
	agora := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)
	u := uuid.New()

	semRepo := tenancy.NewService(repoQueFalha{t: t}, clock.NewFake(agora))
	if _, err := semRepo.Create(context.Background(), u, "  "); !errors.Is(err, tenancy.ErrProjectNameRequired) {
		t.Errorf("Create(\"  \") = %v, queria ErrProjectNameRequired", err)
	}

	repo := &repoFalso{}
	svc := tenancy.NewService(repo, clock.NewFake(agora))
	resumo, err := svc.Create(context.Background(), u, " X ")
	if err != nil {
		t.Fatal(err)
	}
	got := repo.novoRecebido
	if got.Name != "X" || got.TitularID != u || !got.Now.Equal(agora) || got.ID == uuid.Nil {
		t.Errorf("repassado = %+v, queria Name \"X\", titular %s, Now %v e id novo", got, u, agora)
	}
	if resumo.ID != got.ID {
		t.Errorf("resumo.ID = %s, queria o do repositório %s", resumo.ID, got.ID)
	}
}

// Papel desconhecido para no Service: o repositório não é chamado. O papel
// válido segue convertido.
func TestServiceChangeRoleRejectsUnknownRoleBeforeRepo(t *testing.T) {
	semRepo := tenancy.NewService(repoQueFalha{t: t}, clock.NewFake(time.Now()))
	_, err := semRepo.ChangeRole(context.Background(), uuid.New(), uuid.New(), uuid.New(), "admin")
	if !errors.Is(err, tenancy.ErrUnknownRole) {
		t.Errorf("ChangeRole(\"admin\") = %v, queria ErrUnknownRole", err)
	}

	repo := &repoFalso{}
	svc := tenancy.NewService(repo, clock.NewFake(time.Now()))
	if _, err := svc.ChangeRole(context.Background(), uuid.New(), uuid.New(), uuid.New(), "viewer"); err != nil {
		t.Fatal(err)
	}
	if repo.papelRecebido != tenancy.RoleViewer {
		t.Errorf("papel repassado = %q, queria viewer", repo.papelRecebido)
	}
}

// O nome é validado e aparado antes do repositório, como na criação.
func TestServiceRenameValidatesName(t *testing.T) {
	semRepo := tenancy.NewService(repoQueFalha{t: t}, clock.NewFake(time.Now()))
	if _, err := semRepo.Rename(context.Background(), uuid.New(), uuid.New(), " "); !errors.Is(err, tenancy.ErrProjectNameRequired) {
		t.Errorf("Rename(\" \") = %v, queria ErrProjectNameRequired", err)
	}

	repo := &repoFalso{}
	svc := tenancy.NewService(repo, clock.NewFake(time.Now()))
	if _, err := svc.Rename(context.Background(), uuid.New(), uuid.New(), "  Novo  "); err != nil {
		t.Fatal(err)
	}
	if repo.nomeRecebido != "Novo" {
		t.Errorf("nome repassado = %q, queria \"Novo\"", repo.nomeRecebido)
	}
}
