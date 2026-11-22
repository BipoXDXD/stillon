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

// inserirProjeto grava um projeto com SQL cru no escopo de quem o cria.
func inserirProjeto(t *testing.T, pool *pgxpool.Pool, escopo, titular uuid.UUID, padrao bool) (uuid.UUID, error) {
	t.Helper()
	id := novoID()
	err := emEscopo(t, pool, "app.user_id", escopo.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO projects (id, name, titular_id, is_default, created_at) VALUES ($1, 'Projeto', $2, $3, $4)`,
			id, titular, padrao, agora)
		return err
	})
	return id, err
}

// novoProjeto cria um projeto do titular, no escopo dele, sem o vínculo.
func novoProjeto(t *testing.T, pool *pgxpool.Pool, titular uuid.UUID, padrao bool) uuid.UUID {
	t.Helper()
	id, err := inserirProjeto(t, pool, titular, titular, padrao)
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	return id
}

// inserirVinculo grava um vínculo com SQL cru no escopo de quem o cria.
func inserirVinculo(t *testing.T, pool *pgxpool.Pool, escopo, projeto, membro uuid.UUID, papel string) error {
	t.Helper()
	return emEscopo(t, pool, "app.user_id", escopo.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(),
			`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)`,
			projeto, membro, papel, agora)
		return err
	})
}

func TestProjectsVisibleOnlyToMembersAndTitular(t *testing.T) {
	pool := testdb.New(t)
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	pa := novoProjeto(t, pool, a, true)
	if err := inserirVinculo(t, pool, a, pa, a, "owner"); err != nil {
		t.Fatalf("A não conseguiu se vincular como owner ao próprio projeto: %v", err)
	}

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
	a, b := novoUsuario(t, pool), novoUsuario(t, pool)
	// Sem o vínculo de A ainda: cada recusa abaixo só pode vir da política, e
	// não da chave primária (project_id, user_id).
	pa := novoProjeto(t, pool, a, true)

	casos := []struct {
		nome           string
		escopo, membro uuid.UUID
		papel          string
	}{
		{"B se inclui como owner no projeto de A", b, b, "owner"},
		{"A inclui B (convite é do PR 4)", a, b, "viewer"},
		{"A entra no próprio projeto sem ser owner", a, a, "manager"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := inserirVinculo(t, pool, c.escopo, pa, c.membro, c.papel)
			if codigoPG(err) != "42501" {
				t.Errorf("deu %v, queria 42501 (RLS)", err)
			}
		})
	}

	t.Run("B cria projeto em nome de A", func(t *testing.T) {
		_, err := inserirProjeto(t, pool, b, a, false)
		if codigoPG(err) != "42501" {
			t.Errorf("deu %v, queria 42501 (RLS)", err)
		}
	})

	// Controle: o caminho legítimo passa, então as recusas acima vieram das
	// regras que cada caso quebra, e não de outra coisa.
	if err := inserirVinculo(t, pool, a, pa, a, "owner"); err != nil {
		t.Fatalf("A não conseguiu se vincular como owner ao próprio projeto: %v", err)
	}
}

func TestProjectMemberRoleCheckRejectsUnknownRole(t *testing.T) {
	pool := testdb.New(t)
	a := novoUsuario(t, pool)
	pa := novoProjeto(t, pool, a, true)

	_, err := testdb.Admin(t).Exec(context.Background(),
		`INSERT INTO project_members (project_id, user_id, role, created_at) VALUES ($1, $2, 'admin', $3)`,
		pa, a, agora)
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
