package testdb

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// ProjetoPadrao devolve o Projeto padrão do usuário, criando-o com o vínculo
// de owner se ainda não existir. Grava como superusuário (Admin), por fora da
// RLS, como a migration de backfill faria; chamar de novo devolve o mesmo id.
// Projeto e vínculo vão numa transação só: a FK adiável da 00048 (o titular
// é sempre owner) confere no commit.
func ProjetoPadrao(t *testing.T, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	novo, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	var projeto uuid.UUID
	err = pgx.BeginFunc(ctx, Admin(t), func(tx pgx.Tx) error {
		// O SELECT do UNION não enxerga a linha que o CTE insere: sai uma
		// linha só, a nova ou a que já existia.
		if err := tx.QueryRow(ctx, `
			WITH novo AS (
			    INSERT INTO projects (id, name, titular_id, is_default, created_at)
			    VALUES ($1, 'Projeto padrão', $2, true, now())
			    ON CONFLICT (titular_id) WHERE is_default DO NOTHING
			    RETURNING id)
			SELECT id FROM novo
			UNION ALL
			SELECT id FROM projects WHERE titular_id = $2 AND is_default`, novo, userID).Scan(&projeto); err != nil {
			return fmt.Errorf("criar o Projeto padrão: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO project_members (project_id, user_id, role, created_at)
			VALUES ($1, $2, 'owner', now())
			ON CONFLICT (project_id, user_id) DO NOTHING`, projeto, userID); err != nil {
			return fmt.Errorf("vincular o titular ao Projeto padrão: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return projeto
}

// NoProjetoPadrao garante o Projeto padrão de userID e roda fn em
// storage.InProjectTx nele; projeto vai para os Insert* que pedem project_id.
//
// As sementes de recurso dos testes passam por aqui, e não por
// storage.InUserTx: com as políticas por pertença ao projeto, o escopo de
// usuário não vê nem grava recurso nenhum.
func NoProjetoPadrao(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID,
	fn func(q *db.Queries, projeto uuid.UUID) error) error {
	t.Helper()
	return noProjetoPadrao(t, pool, userID, fn)
}

// noProjetoPadrao aceita qualquer storage.Beginner para o teste do helper
// guardar a transação e ler o app.project_id nela.
func noProjetoPadrao(t *testing.T, b storage.Beginner, userID uuid.UUID,
	fn func(q *db.Queries, projeto uuid.UUID) error) error {
	t.Helper()
	projeto := ProjetoPadrao(t, userID)
	return storage.InProjectTx(context.Background(), b, userID, projeto,
		func(q *db.Queries, _ tenancy.ProjectScope) error { return fn(q, projeto) })
}

// EscopoDoProjetoPadrao liga, na transação tx já aberta, o escopo do Projeto
// padrão de userID, e devolve o projeto: para o SQL de teste sem query gerada
// e para a transação que o teste segura aberta (trava de linha, concorrência).
// Passa por storage.InProjectPgxTx num savepoint de tx: a mesma conferência de
// pertença, e os set_config locais valem para tx depois que o savepoint fecha.
func EscopoDoProjetoPadrao(t *testing.T, tx pgx.Tx, userID uuid.UUID) uuid.UUID {
	t.Helper()
	projeto := ProjetoPadrao(t, userID)
	if err := storage.InProjectPgxTx(context.Background(), tx, userID, projeto,
		func(pgx.Tx, tenancy.ProjectScope) error { return nil }); err != nil {
		t.Fatalf("ligar o escopo do Projeto padrão de %s: %v", userID, err)
	}
	return projeto
}

// NovoProjeto cria um projeto que não é o padrão, com titular como owner, e
// devolve o id. Grava como superusuário (Admin), por fora da RLS, projeto e
// vínculo numa transação só (FK adiável da 00048).
func NovoProjeto(t *testing.T, titular uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	projeto, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	err = pgx.BeginFunc(ctx, Admin(t), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO projects (id, name, titular_id, is_default, created_at)
			VALUES ($1, $2, $3, false, now())`, projeto, nome, titular); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO project_members (project_id, user_id, role, created_at)
			VALUES ($1, $2, 'owner', now())`, projeto, titular)
		return err
	})
	if err != nil {
		t.Fatalf("criar o projeto %q: %v", nome, err)
	}
	return projeto
}

// Membro vincula usuario ao projeto com o papel dado (owner, manager, member
// ou viewer), por fora da RLS.
func Membro(t *testing.T, projeto, usuario uuid.UUID, papel string) {
	t.Helper()
	if _, err := Admin(t).Exec(context.Background(), `
		INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES ($1, $2, $3, now())`, projeto, usuario, papel); err != nil {
		t.Fatalf("vincular %s ao projeto %s como %s: %v", usuario, projeto, papel, err)
	}
}

// Suspender marca a conta como suspensa agora, por fora da RLS.
func Suspender(t *testing.T, usuario uuid.UUID) {
	t.Helper()
	tag, err := Admin(t).Exec(context.Background(),
		`UPDATE users SET suspended_at = now() WHERE id = $1`, usuario)
	if err != nil {
		t.Fatalf("suspender %s: %v", usuario, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("suspender %s: usuário não existe", usuario)
	}
}

// Operador marca a conta como operadora (sem as cotas do plano grátis), por
// fora da RLS.
func Operador(t *testing.T, usuario uuid.UUID) {
	t.Helper()
	tag, err := Admin(t).Exec(context.Background(),
		`UPDATE users SET is_operator = true WHERE id = $1`, usuario)
	if err != nil {
		t.Fatalf("tornar %s operador: %v", usuario, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("tornar %s operador: usuário não existe", usuario)
	}
}

// ProjetoDoAlvo lê, por fora da RLS, o projeto em que o alvo vive. Falha se o
// alvo não tem projeto: o gatilho só o preenche quando o dono já tem o
// Projeto padrão (chame ProjetoPadrao antes de criar o alvo).
func ProjetoDoAlvo(t *testing.T, alvo uuid.UUID) uuid.UUID {
	t.Helper()
	var projeto *uuid.UUID
	if err := Admin(t).QueryRow(context.Background(),
		`SELECT project_id FROM targets WHERE id = $1`, alvo).Scan(&projeto); err != nil {
		t.Fatalf("ler o projeto do alvo %s: %v", alvo, err)
	}
	if projeto == nil {
		t.Fatalf("o alvo %s não tem projeto: crie o Projeto padrão do dono antes do alvo", alvo)
	}
	return *projeto
}
