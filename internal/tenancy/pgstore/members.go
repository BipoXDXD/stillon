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
// autorização pelo papel de actorID lido na transação.
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
// transação, os canais de e-mail que ele criou ali param de avisar.
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
		out = tenancy.ProjectSummary{ID: l.ID, Name: l.Name, IsDefault: l.IsDefault, Role: sc.Role, CreatedAt: l.CreatedAt}
		return nil
	})
	if err != nil {
		return tenancy.ProjectSummary{}, err
	}
	return out, nil
}

// mutarMembro é o caminho de toda mutação de membro: escopo do projeto, trava
// dos membros, leitura do alvo (ErrNotMember sem ele) e fn com o escopo da
// transação. A FK adiável do titular só confere no commit: se ela recusar,
// o erro vira ErrTitularProtected (defesa; o domínio já recusa antes).
func (s *Store) mutarMembro(ctx context.Context, actorID, projectID, targetID uuid.UUID,
	fn func(*db.Queries, tenancy.ProjectScope, tenancy.MemberView) error) error {
	err := storage.InProjectTx(ctx, s.pool, actorID, projectID, func(q *db.Queries, sc tenancy.ProjectScope) error {
		if err := q.LockProjectMembers(ctx, projectID); err != nil {
			return fmt.Errorf("travar membros do projeto: %w", err)
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

// membro monta o MemberView de uma linha de project_members.
func membro(id uuid.UUID, email, papel string, titular bool, desde time.Time) (tenancy.MemberView, error) {
	r, err := tenancy.ParseRole(papel)
	if err != nil {
		// O CHECK de project_members só aceita papéis conhecidos.
		return tenancy.MemberView{}, fmt.Errorf("papel gravado em project_members: %w", err)
	}
	return tenancy.MemberView{UserID: id, Email: email, Role: r, IsTitular: titular, JoinedAt: desde}, nil
}
