// Package pgstore é o adaptador Postgres da porta tenancy.ProjectRepository.
// Fica fora de tenancy para o domínio não importar pgx nem storage.
package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// Store implementa tenancy.ProjectRepository sobre o pool da aplicação, sob a
// RLS: toda leitura e escrita passa por um escopo de storage.
type Store struct {
	pool *pgxpool.Pool
}

var _ tenancy.ProjectRepository = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Membership confere a pertença com storage.InProjectTx, que aplica as regras
// de suspensão e devolve tenancy.ErrProjectNotFound sem pertença.
func (s *Store) Membership(ctx context.Context, userID, projectID uuid.UUID) (tenancy.ProjectScope, error) {
	var escopo tenancy.ProjectScope
	err := storage.InProjectTx(ctx, s.pool, userID, projectID, func(_ *db.Queries, sc tenancy.ProjectScope) error {
		escopo = sc
		return nil
	})
	if err != nil {
		return tenancy.ProjectScope{}, err
	}
	return escopo, nil
}

// EnsureDefault devolve o escopo no Projeto padrão de userID, criando-o se
// preciso. As rotas antigas chamam isto a cada requisição, então o caminho
// rápido só lê: achou o padrão, o escopo sai de Membership, que aplica as
// regras de suspensão. Um padrão sem owner não se repara aqui (Membership o
// recusaria), mas isso é inalcançável: o padrão nasce com o owner na mesma
// transação, e o app não tem UPDATE nem DELETE em projects e
// project_members. Sem padrão, o caminho lento cria o projeto e o vínculo
// de owner na mesma transação (o banco não garante que todo projeto tenha
// owner). O índice único parcial faz a chamada concorrente não criar outro: o
// ON CONFLICT espera a primeira transação e, em READ COMMITTED, o SELECT
// seguinte já enxerga o projeto dela.
func (s *Store) EnsureDefault(ctx context.Context, userID uuid.UUID, now time.Time) (tenancy.ProjectScope, error) {
	var existente uuid.UUID
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		existente, err = q.GetDefaultProjectID(ctx, userID)
		return err
	})
	switch {
	case err == nil:
		return s.Membership(ctx, userID, existente)
	case !errors.Is(err, pgx.ErrNoRows):
		return tenancy.ProjectScope{}, fmt.Errorf("ler Projeto padrão: %w", err)
	}
	return s.criarPadrao(ctx, userID, now)
}

// criarPadrao é o caminho lento de EnsureDefault: INSERT ON CONFLICT do
// projeto, leitura do id e vínculo de owner, numa transação só.
func (s *Store) criarPadrao(ctx context.Context, userID uuid.UUID, now time.Time) (tenancy.ProjectScope, error) {
	novo, err := uuid.NewV7()
	if err != nil {
		return tenancy.ProjectScope{}, fmt.Errorf("gerar id do projeto: %w", err)
	}
	var projeto uuid.UUID
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := q.EnsureDefaultProject(ctx, db.EnsureDefaultProjectParams{
			ID: novo, Name: tenancy.DefaultProjectName, TitularID: userID, Now: now,
		}); err != nil {
			return fmt.Errorf("criar Projeto padrão: %w", err)
		}
		projeto, err = q.GetDefaultProjectID(ctx, userID)
		if err != nil {
			return fmt.Errorf("ler Projeto padrão: %w", err)
		}
		if err := q.EnsureProjectOwner(ctx, db.EnsureProjectOwnerParams{
			ProjectID: projeto, UserID: userID, Now: now,
		}); err != nil {
			return fmt.Errorf("vincular owner ao Projeto padrão: %w", err)
		}
		return nil
	})
	if err != nil {
		return tenancy.ProjectScope{}, err
	}
	return s.Membership(ctx, userID, projeto)
}

// ListForUser lê no escopo do usuário os projetos de que ele é membro.
func (s *Store) ListForUser(ctx context.Context, userID uuid.UUID) ([]tenancy.ProjectSummary, error) {
	var linhas []db.ListMyProjectsRow
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		linhas, err = q.ListMyProjects(ctx, userID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("listar projetos: %w", err)
	}
	out := make([]tenancy.ProjectSummary, 0, len(linhas))
	for _, l := range linhas {
		papel, err := tenancy.ParseRole(l.Role)
		if err != nil {
			// O CHECK de project_members só aceita papéis conhecidos.
			return nil, fmt.Errorf("papel gravado em project_members: %w", err)
		}
		out = append(out, tenancy.ProjectSummary{
			ID: l.ID, Name: l.Name, IsDefault: l.IsDefault, Role: papel, CreatedAt: l.CreatedAt,
		})
	}
	return out, nil
}
