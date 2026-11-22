package tenancy

import (
	"context"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// Service é a fachada do contexto de projetos para as camadas de fora.
type Service struct {
	repo ProjectRepository
	clk  clock.Clock
}

func NewService(repo ProjectRepository, clk clock.Clock) *Service {
	return &Service{repo: repo, clk: clk}
}

// Scope devolve o escopo de userID em projectID, ou ErrProjectNotFound.
func (s *Service) Scope(ctx context.Context, userID, projectID uuid.UUID) (ProjectScope, error) {
	return s.repo.Membership(ctx, userID, projectID)
}

// DefaultScope devolve o escopo de userID no Projeto padrão dele, criando o
// projeto se ainda não existir.
func (s *Service) DefaultScope(ctx context.Context, userID uuid.UUID) (ProjectScope, error) {
	return s.repo.EnsureDefault(ctx, userID, s.clk.Now())
}

// List devolve os projetos de que userID é membro.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]ProjectSummary, error) {
	return s.repo.ListForUser(ctx, userID)
}
