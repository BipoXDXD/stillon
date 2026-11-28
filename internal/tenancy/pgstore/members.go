package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// fkTitularOwner é a FK adiável da 00048 que mantém o titular como owner.
const fkTitularOwner = "projects_titular_e_owner"

// ListMembers lê os membros no escopo do projeto: qualquer papel vê a equipe.
func (s *Store) ListMembers(ctx context.Context, actorID, projectID uuid.UUID) ([]tenancy.MemberView, error) {
	var out []tenancy.MemberView
	err := storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		if err := sc.Require(tenancy.PermRead); err != nil {
			return err
		}
		linhas, err := q.ListProjectMembers(ctx, projectID)
		if err != nil {
			return fmt.Errorf("listar membros: %w", err)
		}
		out = make([]tenancy.MemberView, 0, len(linhas))
		for _, l := range linhas {
			m, err := membro(l.UserID, l.Email, l.Role, l.IsTitular, l.CreatedAt)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ChangeRole dá o papel to ao alvo, sob a trava dos membros do projeto, com a
// autorização pelo papel de actorID lido na transação. Na mesma transação,
// somem os convites pendentes do alvo que o papel novo não poderia mandar.
func (s *Store) ChangeRole(ctx context.Context, actorID, projectID, targetID uuid.UUID, to tenancy.Role) (tenancy.MemberView, error) {
	var out tenancy.MemberView
	err := s.mutarMembro(ctx, actorID, projectID, targetID,
		func(q *db.Queries, sc tenancy.ProjectScope, alvo tenancy.MemberView) error {
			if err := tenancy.AuthorizeRoleChange(sc, alvo, to); err != nil {
				return err
			}
			if err := q.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{
				ProjectID: projectID, UserID: targetID, Role: string(to),
			}); err != nil {
				return fmt.Errorf("mudar papel: %w", err)
			}
			if err := apagarConvitesQueNaoPoderiaMandar(ctx, q, projectID, targetID, to); err != nil {
				return err
			}
			out = alvo
			out.Role = to
			return nil
		})
	if err != nil {
		return tenancy.MemberView{}, err
	}
	return out, nil
}

// RemoveMember tira o alvo do projeto (alvo = actorID é sair). Na mesma
// transação, os canais de e-mail que ele criou ali param de avisar, os
// convites que ele aceitou ali somem e os pendentes que ele mandou também.
func (s *Store) RemoveMember(ctx context.Context, actorID, projectID, targetID uuid.UUID) error {
	return s.mutarMembro(ctx, actorID, projectID, targetID,
		func(q *db.Queries, sc tenancy.ProjectScope, alvo tenancy.MemberView) error {
			if err := tenancy.AuthorizeRemoval(sc, alvo); err != nil {
				return err
			}
			if _, err := q.DisableEmailChannelsCreatedBy(ctx, db.DisableEmailChannelsCreatedByParams{
				ProjectID: &projectID, UserID: targetID,
			}); err != nil {
				return fmt.Errorf("desabilitar os canais de e-mail do membro: %w", err)
			}
			// Os convites que ele aceitou ali somem: a política de INSERT por
			// convite (00049) nunca mais o admite sem convite novo.
			if err := q.DeleteAcceptedInvitationsOf(ctx, db.DeleteAcceptedInvitationsOfParams{
				ProjectID: projectID, UserID: targetID,
			}); err != nil {
				return fmt.Errorf("apagar os convites aceitos pelo membro: %w", err)
			}
			// Os pendentes que ele mandou também: o link não sobrevive a quem
			// o assinou.
			if err := q.DeletePendingInvitationsBy(ctx, db.DeletePendingInvitationsByParams{
				ProjectID: projectID, UserID: targetID,
			}); err != nil {
				return fmt.Errorf("apagar os convites pendentes do membro: %w", err)
			}
			n, err := q.DeleteProjectMember(ctx, db.DeleteProjectMemberParams{ProjectID: projectID, UserID: targetID})
			if err != nil {
				return fmt.Errorf("remover membro: %w", err)
			}
			if n != 1 {
				// A trava dos membros serializa as remoções: o alvo lido
				// acima ainda existe.
				return fmt.Errorf("remover membro: %d linhas apagadas, queria 1", n)
			}
			return nil
		})
}

// Rename troca o nome do projeto; renomear é de owner.
func (s *Store) Rename(ctx context.Context, actorID, projectID uuid.UUID, name string) (tenancy.ProjectSummary, error) {
	var out tenancy.ProjectSummary
	err := storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		if err := sc.Require(tenancy.PermManageProject); err != nil {
			return err
		}
		l, err := q.RenameProject(ctx, db.RenameProjectParams{ProjectID: projectID, Name: name})
		if err != nil {
			return fmt.Errorf("renomear projeto: %w", err)
		}
		// Os limites são os do titular, como em ListForUser: o 200 é o mesmo
		// item de GET /api/v1/projects.
		operador, err := q.TitularIsOperator(ctx, projectID)
		if err != nil {
			return fmt.Errorf("ler o titular do projeto: %w", err)
		}
		out = tenancy.ProjectSummary{
			ID: l.ID, Name: l.Name, IsDefault: l.IsDefault, Role: sc.Role, CreatedAt: l.CreatedAt,
			Limites: tenancy.LimitesDe(operador),
		}
		return nil
	})
	if err != nil {
		return tenancy.ProjectSummary{}, err
	}
	return out, nil
}

// mutarMembro é o caminho de toda mutação de membro: escopo do projeto, trava
// dos membros com o papel de quem pede relido sob ela (travarEReler), leitura
// do alvo (ErrNotMember sem ele) e fn com esse escopo. A FK adiável do titular só confere no commit: se
// ela recusar, o erro vira ErrTitularProtected (defesa; o domínio já recusa
// antes).
func (s *Store) mutarMembro(ctx context.Context, actorID, projectID, targetID uuid.UUID,
	fn func(*db.Queries, tenancy.ProjectScope, tenancy.MemberView) error) error {
	err := storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		sc, err := travarEReler(ctx, q, sc)
		if err != nil {
			return err
		}
		l, err := q.GetProjectMember(ctx, db.GetProjectMemberParams{ProjectID: projectID, UserID: targetID})
		if errors.Is(err, pgx.ErrNoRows) {
			return tenancy.ErrNotMember
		}
		if err != nil {
			return fmt.Errorf("ler membro: %w", err)
		}
		alvo, err := membro(l.UserID, l.Email, l.Role, l.IsTitular, l.CreatedAt)
		if err != nil {
			return err
		}
		return fn(q, sc, alvo)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == fkTitularOwner {
		return tenancy.ErrTitularProtected
	}
	return err
}

// apagarConvitesQueNaoPoderiaMandar apaga os pendentes de quem convidou que
// AuthorizeInvite recusaria com o papel novo: rebaixar quem convidou não deixa
// vivo o link de um papel que ele já não pode dar. Roda sob a trava dos
// membros, na transação da mudança de papel.
func apagarConvitesQueNaoPoderiaMandar(ctx context.Context, q *db.Queries, projectID, inviterID uuid.UUID, novo tenancy.Role) error {
	convites, err := q.ListPendingInvitationsBy(ctx, db.ListPendingInvitationsByParams{ProjectID: projectID, UserID: inviterID})
	if err != nil {
		return fmt.Errorf("ler os convites pendentes de quem convidou: %w", err)
	}
	quem := tenancy.ProjectScope{ProjectID: projectID, UserID: inviterID, Role: novo}
	for _, c := range convites {
		papel, err := tenancy.ParseRole(c.Role)
		if err != nil {
			return fmt.Errorf("papel gravado em project_invitations: %w", err)
		}
		if tenancy.AuthorizeInvite(quem, papel) == nil {
			continue
		}
		if _, err := q.DeleteInvitation(ctx, db.DeleteInvitationParams{ProjectID: projectID, ID: c.ID}); err != nil {
			return fmt.Errorf("apagar convite que o papel novo não mandaria: %w", err)
		}
	}
	return nil
}

// travarEReler pega a trava dos membros do projeto do escopo e relê o papel de
// quem pede depois dela: o de sc foi lido antes, e um rebaixamento comitado
// enquanto o pedido esperava seria ignorado. Em READ COMMITTED, o comando
// seguinte à trava já vê o que foi gravado. Sem a linha, quem pede saiu ou
// foi removido nesse meio-tempo (ErrNotMember). Toda mutação de membros e
// convites passa por aqui antes de autorizar.
func travarEReler(ctx context.Context, q *db.Queries, sc tenancy.ProjectScope) (tenancy.ProjectScope, error) {
	if err := q.LockProjectMembers(ctx, sc.ProjectID); err != nil {
		return tenancy.ProjectScope{}, fmt.Errorf("travar membros do projeto: %w", err)
	}
	ator, err := q.GetProjectMember(ctx, db.GetProjectMemberParams{ProjectID: sc.ProjectID, UserID: sc.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.ProjectScope{}, tenancy.ErrNotMember
	}
	if err != nil {
		return tenancy.ProjectScope{}, fmt.Errorf("reler o papel de quem pede: %w", err)
	}
	papel, err := tenancy.ParseRole(ator.Role)
	if err != nil {
		return tenancy.ProjectScope{}, fmt.Errorf("papel gravado em project_members: %w", err)
	}
	sc.Role = papel
	return sc, nil
}

// membro monta o MemberView de uma linha de project_members.
func membro(id uuid.UUID, email, papel string, titular bool, desde time.Time) (tenancy.MemberView, error) {
	r, err := tenancy.ParseRole(papel)
	if err != nil {
		// O CHECK de project_members só aceita papéis conhecidos.
		return tenancy.MemberView{}, fmt.Errorf("papel gravado em project_members: %w", err)
	}
	return tenancy.MemberView{UserID: id, Email: email, Role: r, IsTitular: titular, JoinedAt: desde}, nil
}
