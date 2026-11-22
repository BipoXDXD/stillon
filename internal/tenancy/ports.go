package tenancy

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ProjectSummary é um projeto como aparece na lista de quem é membro dele.
type ProjectSummary struct {
	ID        uuid.UUID
	Name      string
	IsDefault bool
	Role      Role
	CreatedAt time.Time
}

// ProjectRepository é a porta de persistência dos projetos. O adaptador do
// Postgres fica em tenancy/pgstore; o domínio não conhece o banco.
type ProjectRepository interface {
	// Membership devolve o escopo de userID em projectID. Não membro, projeto
	// inexistente e suspensão (do membro ou do titular) dão ErrProjectNotFound.
	Membership(ctx context.Context, userID, projectID uuid.UUID) (ProjectScope, error)
	// EnsureDefault garante o Projeto padrão de userID, com ele como owner, e
	// devolve o escopo nele. Chamadas repetidas ou simultâneas criam um só.
	EnsureDefault(ctx context.Context, userID uuid.UUID, now time.Time) (ProjectScope, error)
	// ListForUser lista os projetos de que userID é membro, o padrão primeiro.
	ListForUser(ctx context.Context, userID uuid.UUID) ([]ProjectSummary, error)
}
