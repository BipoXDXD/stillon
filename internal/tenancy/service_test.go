package tenancy_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// repoFalso guarda o que EnsureDefault recebeu.
type repoFalso struct {
	nowRecebido time.Time
	escopo      tenancy.ProjectScope
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
