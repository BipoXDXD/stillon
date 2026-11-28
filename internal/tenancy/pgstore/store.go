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
	"github.com/BipoXDXD/stillon/internal/targets"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// Store implementa tenancy.ProjectRepository sobre o pool da aplicação, sob a
// RLS: toda leitura e escrita passa por um escopo de storage.
type Store struct {
	pool *pgxpool.Pool
	// tetoPorHost é o teto de alvos com regra de conteúdo por host, o mesmo
	// de targets.Service: o aceite da transferência o confere para quem
	// recebe.
	tetoPorHost int
}

var _ tenancy.ProjectRepository = (*Store)(nil)

// Opcao configura o Store.
type Opcao func(*Store)

// ComTetoDeRegrasPorHost troca o teto de regras de conteúdo por host
// (CONTENT_RULES_PER_HOST), como targets.ComTetoDeRegrasPorHost; sem ela,
// vale targets.DefaultContentRulesPerHost.
func ComTetoDeRegrasPorHost(n int) Opcao {
	return func(s *Store) { s.tetoPorHost = n }
}

func New(pool *pgxpool.Pool, opcoes ...Opcao) *Store {
	s := &Store{pool: pool, tetoPorHost: targets.DefaultContentRulesPerHost}
	for _, o := range opcoes {
		o(s)
	}
	return s
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
// regras de suspensão. Um padrão sem o titular como owner é inalcançável: a
// FK adiável projects_titular_e_owner (00048) recusa no commit o projeto sem
// esse vínculo, e também rebaixar o titular ou apagar o vínculo dele. Sem
// padrão, o caminho lento cria o projeto e o vínculo de owner na mesma
// transação. O índice único parcial faz a chamada concorrente não criar outro: o
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

// criarPadrao é o caminho lento de EnsureDefault: storage.CriarProjetoPadraoNaTx
// (INSERT ON CONFLICT do projeto, leitura do id e vínculo de owner) numa
// transação só, no escopo do usuário.
func (s *Store) criarPadrao(ctx context.Context, userID uuid.UUID, now time.Time) (tenancy.ProjectScope, error) {
	var projeto uuid.UUID
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		projeto, err = storage.CriarProjetoPadraoNaTx(ctx, q, userID, now)
		return err
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
			Limites:        tenancy.LimitesDe(l.TitularIsOperator),
			PendingTitular: l.PendingTitular,
		})
	}
	return out, nil
}

// Create grava o projeto e o vínculo owner do titular numa só transação, no
// escopo dele. A trava stillon.projects:<titular> vem antes da contagem: sem
// ela, duas criações simultâneas contariam o mesmo total e passariam do teto.
// O vínculo vai na mesma transação: a FK adiável projects_titular_e_owner
// (00048) confere no commit que o titular é owner.
func (s *Store) Create(ctx context.Context, p tenancy.NewProject) (tenancy.ProjectSummary, error) {
	var (
		linha    db.InsertProjectRow
		operador bool
	)
	err := storage.InUserTx(ctx, s.pool, p.TitularID, func(q *db.Queries) error {
		if err := q.LockTitularProjects(ctx, p.TitularID); err != nil {
			return fmt.Errorf("travar projetos do titular: %w", err)
		}
		var err error
		operador, err = q.UserIsOperator(ctx, p.TitularID)
		if err != nil {
			return fmt.Errorf("ler se o titular é operador: %w", err)
		}
		total, err := q.CountTitularProjects(ctx, p.TitularID)
		if err != nil {
			return fmt.Errorf("contar projetos do titular: %w", err)
		}
		if total >= int64(tenancy.LimitesDe(operador).Projetos) {
			return tenancy.ErrProjectLimit
		}
		linha, err = q.InsertProject(ctx, db.InsertProjectParams{
			ID: p.ID, Name: p.Name, TitularID: p.TitularID, Now: p.Now,
		})
		if err != nil {
			return fmt.Errorf("criar projeto: %w", err)
		}
		if err := q.InsertProjectOwner(ctx, db.InsertProjectOwnerParams{
			ProjectID: p.ID, UserID: p.TitularID, Now: p.Now,
		}); err != nil {
			return fmt.Errorf("vincular owner ao projeto: %w", err)
		}
		return nil
	})
	if err != nil {
		return tenancy.ProjectSummary{}, err
	}
	return tenancy.ProjectSummary{
		ID: linha.ID, Name: linha.Name, IsDefault: linha.IsDefault,
		Role: tenancy.RoleOwner, CreatedAt: linha.CreatedAt,
		Limites: tenancy.LimitesDe(operador),
	}, nil
}
