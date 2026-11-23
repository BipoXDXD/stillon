package tenancy

import (
	"context"
	"fmt"

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

// Scope devolve o escopo de userID em projectID, ou ErrProjectNotFound. O UUID
// nulo nunca é projeto: sai ErrProjectNotFound sem ir ao repositório (o
// middleware já o barra; aqui é a defesa do domínio).
func (s *Service) Scope(ctx context.Context, userID, projectID uuid.UUID) (ProjectScope, error) {
	if projectID == uuid.Nil {
		return ProjectScope{}, ErrProjectNotFound
	}
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

// Create cria um projeto de que userID é titular e owner. O nome é validado
// antes do repositório; a cota do titular é conferida lá, sob a trava dele.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, name string) (ProjectSummary, error) {
	nome, err := ParseProjectName(name)
	if err != nil {
		return ProjectSummary{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return ProjectSummary{}, fmt.Errorf("gerar id do projeto: %w", err)
	}
	return s.repo.Create(ctx, NewProject{ID: id, Name: nome, TitularID: userID, Now: s.clk.Now()})
}
