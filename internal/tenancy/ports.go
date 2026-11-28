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
	// Limites são os do titular do projeto, não os de quem pede a lista.
	Limites Limites
	// PendingTitular: a titularidade foi proposta a quem pede a lista, que
	// pode aceitar ou recusar. Só quem recebe vê a proposta aqui.
	PendingTitular bool
}

// NewProject é o projeto a criar pela API: nunca o padrão.
type NewProject struct {
	ID        uuid.UUID
	Name      string // já normalizado por ParseProjectName
	TitularID uuid.UUID
	Now       time.Time
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
	// Create grava o projeto (is_default = false) e o vínculo owner do titular
	// na MESMA transação, sob a trava do titular; ErrProjectLimit quando
	// LimitesDe(is_operator).Projetos já foi atingido.
	Create(ctx context.Context, p NewProject) (ProjectSummary, error)

	// As operações de membros e de renomear rodam no escopo do projeto,
	// agindo como actorID: não membro dá ErrProjectNotFound, e a autorização
	// (Authorize*, Require) usa o papel lido na própria transação, nunca o de
	// quem chamou.

	// ListMembers lista os membros, o titular primeiro e depois por entrada.
	ListMembers(ctx context.Context, actorID, projectID uuid.UUID) ([]MemberView, error)
	// ChangeRole dá o papel to a targetID. Alvo que não é membro dá
	// ErrNotMember.
	ChangeRole(ctx context.Context, actorID, projectID, targetID uuid.UUID, to Role) (MemberView, error)
	// RemoveMember tira targetID do projeto (targetID = actorID é sair) e
	// desabilita, na mesma transação, os canais de e-mail que ele criou ali.
	RemoveMember(ctx context.Context, actorID, projectID, targetID uuid.UUID) error
	// Rename troca o nome do projeto (name já normalizado).
	Rename(ctx context.Context, actorID, projectID uuid.UUID, name string) (ProjectSummary, error)

	// A transferência de titularidade (decisão D2), sob a trava dos membros.

	// ProposeTransfer propõe a titularidade a targetID (só o titular; o alvo
	// tem de ser outro owner, e o Projeto padrão não vai: ErrTransferTarget).
	// Uma proposta nova substitui a anterior.
	ProposeTransfer(ctx context.Context, actorID, projectID, targetID uuid.UUID) error
	// AcceptTransfer faz de userID, o pendente, o titular, se a cota dele
	// comporta o projeto: ErrProjectLimit, ErrMemberLimit ou o erro de teto de
	// alvos, heartbeats ou canais. Sem proposta para ele, ErrForbidden.
	AcceptTransfer(ctx context.Context, userID, projectID uuid.UUID) (ProjectSummary, error)
	// CancelTransfer desfaz a proposta: o titular cancela, o pendente recusa;
	// outro membro recebe ErrForbidden.
	CancelTransfer(ctx context.Context, userID, projectID uuid.UUID) error
}
