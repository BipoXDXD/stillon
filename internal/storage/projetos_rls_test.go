package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// inserirProjeto grava, com SQL cru no escopo de quem o cria, um projeto e o
// vínculo de owner do titular, na mesma transação: a FK adiável da 00048 (o
// titular é sempre owner) recusa no commit o projeto sem esse vínculo.
func inserirProjeto(t *testing.T, pool *pgxpool.Pool, escopo, titular uuid.UUID, padrao bool) (uuid.UUID, error) {
	t.Helper()
	id := novoID()
	err := emEscopo(t, pool, "app.user_id", escopo.String(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(),
			`INSERT INTO projects (id, name, titular_id, is_default, created_at) VALUES ($1, 'Projeto', $2, $3, $4)`,
			id, titular, padrao, agora); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(),
			`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1, $2, 'owner', $3)`,
			id, titular, agora)
		return err
	})
	return id, err
}

// novoProjeto cria um projeto do titular, no escopo dele, com o vínculo de owner.
func novoProjeto(t *testing.T, pool *pgxpool.Pool, titular uuid.UUID, padrao bool) uuid.UUID {
	t.Helper()
	id, err := inserirProjeto(t, pool, titular, titular, padrao)
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	return id
}

func TestProjectsVisibleOnlyToMembersAndTitular(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoProjeto(t, pool, a, true)

	if n := contarComEscopo(t, pool, a, "projects"); n != 1 {
		t.Errorf("A vê %d projetos, queria 1", n)
	}
	if n := contarComEscopo(t, pool, a, "project_members"); n != 1 {
		t.Errorf("A vê %d vínculos, queria 1", n)
	}
	if n := contarComEscopo(t, pool, b, "projects"); n != 0 {
		t.Errorf("B vê %d projetos de A", n)
	}
	if n := contarComEscopo(t, pool, b, "project_members"); n != 0 {
		t.Errorf("B vê %d vínculos de A", n)
	}
}

func TestMemberCannotJoinAnotherTitularsProject(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)

	// Tudo numa transação só, no escopo de A, com o projeto ainda sem o
	// vínculo dele: cada recusa abaixo só pode vir da política, e não da
	// chave primária (project_id, user_id). Cada caso roda num savepoint com
	// o escopo do caso; a FK adiável da 00048 só confere no commit, e o
	// controle no fim grava o vínculo de owner antes dele.
	pa := novoID()
	casos := []struct {
		nome           string
		escopo, membro uuid.UUID
		papel          string
	}{
		{"B se inclui como owner no projeto de A", b, b, "owner"},
		{"A inclui B (convite é do PR 4)", a, b, "viewer"},
		{"A entra no próprio projeto sem ser owner", a, a, "manager"},
	}
	err := emEscopo(t, pool, "app.user_id", a.String(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO projects (id, name, titular_id, is_default, created_at) VALUES ($1, 'Projeto', $2, true, $3)`,
			pa, a, agora); err != nil {
			t.Fatalf("criar projeto: %v", err)
		}
		for _, c := range casos {
			t.Run(c.nome, func(t *testing.T) {
				if _, err := tx.Exec(ctx, `SAVEPOINT caso`); err != nil {
					t.Fatal(err)
				}
				_, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, c.escopo.String())
				if err == nil {
					_, err = tx.Exec(ctx,
						`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)`,
						pa, c.membro, c.papel, agora)
				}
				if codigoPG(err) != "42501" {
					t.Errorf("deu %v, queria 42501 (RLS)", err)
				}
				if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT caso`); err != nil {
					t.Fatal(err)
				}
			})
		}
		// Controle: o caminho legítimo passa, então as recusas acima vieram
		// das regras que cada caso quebra, e não de outra coisa.
		_, err := tx.Exec(ctx,
			`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1, $2, 'owner', $3)`,
			pa, a, agora)
		return err
	})
	if err != nil {
		t.Fatalf("A não conseguiu se vincular como owner ao próprio projeto: %v", err)
	}

	t.Run("B cria projeto em nome de A", func(t *testing.T) {
		_, err := inserirProjeto(t, pool, b, a, false)
		if codigoPG(err) != "42501" {
			t.Errorf("deu %v, queria 42501 (RLS)", err)
		}
	})
}

func TestProjectMemberRoleCheckRejectsUnknownRole(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	pa := novoProjeto(t, pool, a, true)

	_, err := testdb.Admin(t).Exec(context.Background(),
		`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1, $2, 'admin', $3)`,
		pa, b, agora)
	if !violaCheck(err) {
		t.Errorf("role 'admin' deu %v, queria violação de CHECK (23514)", err)
	}
}

func TestOnlyOneDefaultProjectPerTitular(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	novoProjeto(t, pool, a, true)

	if _, err := inserirProjeto(t, pool, a, a, true); codigoPG(err) != "23505" {
		t.Errorf("segundo projeto padrão de A deu %v, queria 23505", err)
	}
	if _, err := inserirProjeto(t, pool, a, a, false); err != nil {
		t.Errorf("segundo projeto não padrão de A: %v", err)
	}
	// O índice é por titular: o padrão de B não esbarra no de A.
	if _, err := inserirProjeto(t, pool, b, b, true); err != nil {
		t.Errorf("projeto padrão de B: %v", err)
	}
}

func TestUsersStartAsNonOperatorUnverifiedActive(t *testing.T) {
	pool := testdb.New(t)
	a := novoUsuario(t, pool)

	var operador bool
	var verificado, suspenso *time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT is_operator, email_verified_at, suspended_at FROM users WHERE id = $1`, a).
		Scan(&operador, &verificado, &suspenso)
	if err != nil {
		t.Fatal(err)
	}
	if operador {
		t.Error("usuário novo nasceu operador")
	}
	if verificado != nil {
		t.Errorf("usuário novo nasceu com e-mail verificado em %v", verificado)
	}
	if suspenso != nil {
		t.Errorf("usuário novo nasceu suspenso em %v", suspenso)
	}
}
