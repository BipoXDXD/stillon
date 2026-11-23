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

// repoFalso guarda o que EnsureDefault e Create receberam.
type repoFalso struct {
	nowRecebido  time.Time
	escopo       tenancy.ProjectScope
	novoRecebido tenancy.NewProject
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
