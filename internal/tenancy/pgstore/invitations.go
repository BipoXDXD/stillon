package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// Invitations implementa tenancy.InvitationRepository sob a RLS: criar,
// listar e revogar no escopo do projeto; espiar e aceitar nos escopos do
// link (00049).
type Invitations struct {
	pool *pgxpool.Pool
}

var _ tenancy.InvitationRepository = (*Invitations)(nil)

func NewInvitations(pool *pgxpool.Pool) *Invitations {
	return &Invitations{pool: pool}
}

// Create grava o convite sob a trava dos membros do projeto, com a
// autorização pelo papel de actorID relido depois dela. A trava serializa a
// contagem do teto: dois convites na última vaga não passam juntos.
func (s *Invitations) Create(ctx context.Context, actorID, projectID uuid.UUID, inv tenancy.NewInvitation) (tenancy.Invitation, tenancy.InvitationEmail, error) {
	var (
		out tenancy.Invitation
		msg tenancy.InvitationEmail
	)
	err := storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		sc, err := travarEReler(ctx, q, sc)
		if err != nil {
			return err
		}
		if err := tenancy.AuthorizeInvite(sc, inv.Role); err != nil {
			return err
		}
		if err := q.DeleteStaleInvitations(ctx, db.DeleteStaleInvitationsParams{ProjectID: projectID, Now: inv.Now}); err != nil {
			return fmt.Errorf("apagar convites velhos: %w", err)
		}
		membro, err := q.IsMemberEmail(ctx, db.IsMemberEmailParams{ProjectID: projectID, Email: inv.Email})
		if err != nil {
			return fmt.Errorf("conferir se o e-mail já é membro: %w", err)
		}
		if membro {
			return tenancy.ErrAlreadyMember
		}
		// Reenvio: o pendente anterior do mesmo e-mail sai antes da contagem,
		// e o novo ocupa a mesma vaga.
		if err := q.DeletePendingInvitationForEmail(ctx, db.DeletePendingInvitationForEmailParams{
			ProjectID: projectID, Email: inv.Email,
		}); err != nil {
			return fmt.Errorf("apagar o convite anterior: %w", err)
		}
		if err := conferirTetoDeMembros(ctx, q, projectID, inv.Now); err != nil {
			return err
		}
		l, err := q.InsertInvitation(ctx, db.InsertInvitationParams{
			ID: inv.ID, ProjectID: projectID, Email: inv.Email, Role: string(inv.Role), TokenHash: inv.TokenHash,
			InvitedBy: &actorID, CreatedAt: inv.Now, ExpiresAt: tenancy.InvitationExpiry(inv.Now),
		})
		if err != nil {
			return fmt.Errorf("gravar convite: %w", err)
		}
		if out, err = convite(l.ID, l.ProjectID, l.Email, l.Role, l.CreatedAt, l.ExpiresAt); err != nil {
			return err
		}
		ctxDoEmail, err := q.InvitationContext(ctx, db.InvitationContextParams{ProjectID: projectID, Inviter: actorID})
		if err != nil {
			return fmt.Errorf("ler o projeto e quem convida: %w", err)
		}
		msg = tenancy.InvitationEmail{ProjectName: ctxDoEmail.ProjectName, InviterEmail: ctxDoEmail.InviterEmail}
		return nil
	})
	if err != nil {
		return tenancy.Invitation{}, tenancy.InvitationEmail{}, err
	}
	return out, msg, nil
}

// conferirTetoDeMembros dá ErrMemberLimit quando membros além do titular e
// pendentes válidos já chegaram ao teto do titular. Roda sob a trava.
func conferirTetoDeMembros(ctx context.Context, q *db.Queries, projectID uuid.UUID, now time.Time) error {
	operador, err := q.TitularIsOperator(ctx, projectID)
	if err != nil {
		return fmt.Errorf("ler o titular do projeto: %w", err)
	}
	membros, err := q.CountMembersBesidesTitular(ctx, projectID)
	if err != nil {
		return fmt.Errorf("contar membros: %w", err)
	}
	pendentes, err := q.CountPendingInvitations(ctx, db.CountPendingInvitationsParams{ProjectID: projectID, Now: now})
	if err != nil {
		return fmt.Errorf("contar convites pendentes: %w", err)
	}
	if membros+pendentes >= int64(tenancy.MemberLimit(operador)) {
		return tenancy.ErrMemberLimit
	}
	return nil
}

// ListPending lista os pendentes válidos; ver convites é de manager para
// cima. Só lê: o papel de InProjectTx basta.
func (s *Invitations) ListPending(ctx context.Context, actorID, projectID uuid.UUID, now time.Time) ([]tenancy.Invitation, error) {
	var out []tenancy.Invitation
	err := storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		if err := sc.Require(tenancy.PermInviteBasic); err != nil {
			return err
		}
		linhas, err := q.ListPendingInvitations(ctx, db.ListPendingInvitationsParams{ProjectID: projectID, Now: now})
		if err != nil {
			return fmt.Errorf("listar convites: %w", err)
		}
		out = make([]tenancy.Invitation, 0, len(linhas))
		for _, l := range linhas {
			c, err := convite(l.ID, l.ProjectID, l.Email, l.Role, l.CreatedAt, l.ExpiresAt)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Revoke apaga o pendente sob a trava, com o papel relido depois dela. Quem
// não convida nada recebe ErrForbidden antes de saber se o convite existe; o
// papel do convite apagado passa por AuthorizeInvite, e a recusa desfaz o
// DELETE com a transação.
func (s *Invitations) Revoke(ctx context.Context, actorID, projectID, invitationID uuid.UUID) error {
	return storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		sc, err := travarEReler(ctx, q, sc)
		if err != nil {
			return err
		}
		if err := sc.Require(tenancy.PermInviteBasic); err != nil {
			return err
		}
		texto, err := q.DeleteInvitation(ctx, db.DeleteInvitationParams{ProjectID: projectID, ID: invitationID})
		if errors.Is(err, pgx.ErrNoRows) {
			return tenancy.ErrInvitationNotFound
		}
		if err != nil {
			return fmt.Errorf("revogar convite: %w", err)
		}
		papel, err := tenancy.ParseRole(texto)
		if err != nil {
			return fmt.Errorf("papel gravado em project_invitations: %w", err)
		}
		return tenancy.AuthorizeInvite(sc, papel)
	})
}

// Accept espia o convite do hash e confere o e-mail da conta (o erro de outra
// conta só sai com o link provado válido); depois, no escopo do aceite e sob
// a trava dos membros, consome o convite e cria o vínculo na mesma transação.
// O consumo é um UPDATE só: o segundo aceite simultâneo não acha linha.
func (s *Invitations) Accept(ctx context.Context, userID uuid.UUID, tokenHash []byte, now time.Time) (tenancy.ProjectSummary, error) {
	var espiado db.PeekInvitationRow
	err := storage.InInvitationTokenTx(ctx, s.pool, tokenHash, func(q *db.Queries) error {
		var err error
		espiado, err = q.PeekInvitation(ctx, db.PeekInvitationParams{TokenHash: tokenHash, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return tenancy.ErrInvitationInvalid
		}
		if err != nil {
			return fmt.Errorf("espiar convite: %w", err)
		}
		conta, err := q.GetAccount(ctx, userID)
		if err != nil {
			return fmt.Errorf("ler a conta de quem aceita: %w", err)
		}
		if strings.ToLower(conta.Email) != espiado.Email {
			return tenancy.ErrInvitationOtherAccount
		}
		return nil
	})
	if err != nil {
		return tenancy.ProjectSummary{}, err
	}

	var out tenancy.ProjectSummary
	err = storage.InInvitationAcceptTx(ctx, s.pool, userID, tokenHash, func(q *db.Queries) error {
		if err := q.LockProjectMembers(ctx, espiado.ProjectID); err != nil {
			return fmt.Errorf("travar membros do projeto: %w", err)
		}
		aceito, err := q.AcceptInvitation(ctx, db.AcceptInvitationParams{TokenHash: tokenHash, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			// Revogado, trocado ou aceito enquanto este pedido esperava.
			return tenancy.ErrInvitationInvalid
		}
		if err != nil {
			return fmt.Errorf("aceitar convite: %w", err)
		}
		if err := q.InsertMemberFromInvitation(ctx, db.InsertMemberFromInvitationParams{
			ProjectID: aceito.ProjectID, UserID: userID, Role: aceito.Role, Now: now,
		}); err != nil {
			return fmt.Errorf("vincular quem aceitou: %w", err)
		}
		l, err := q.GetMyProject(ctx, db.GetMyProjectParams{UserID: userID, ProjectID: aceito.ProjectID})
		if err != nil {
			return fmt.Errorf("ler o projeto do convite: %w", err)
		}
		papel, err := tenancy.ParseRole(l.Role)
		if err != nil {
			return fmt.Errorf("papel gravado em project_members: %w", err)
		}
		out = tenancy.ProjectSummary{
			ID: l.ID, Name: l.Name, IsDefault: l.IsDefault, Role: papel, CreatedAt: l.CreatedAt,
			Limites: tenancy.LimitesDe(l.TitularIsOperator),
		}
		return nil
	})
	if err != nil {
		return tenancy.ProjectSummary{}, err
	}
	return out, nil
}

// convite monta o tenancy.Invitation de uma linha de project_invitations.
func convite(id, projeto uuid.UUID, email, papel string, criado, vence time.Time) (tenancy.Invitation, error) {
	r, err := tenancy.ParseRole(papel)
	if err != nil {
		// O CHECK de project_invitations só aceita papéis conhecidos.
		return tenancy.Invitation{}, fmt.Errorf("papel gravado em project_invitations: %w", err)
	}
	return tenancy.Invitation{ID: id, ProjectID: projeto, Email: email, Role: r, CreatedAt: criado, ExpiresAt: vence}, nil
}
