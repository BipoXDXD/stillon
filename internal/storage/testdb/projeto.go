package testdb

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// ProjetoPadrao devolve o Projeto padrão do usuário, criando-o com o vínculo
// de owner se ainda não existir. Grava como superusuário (Admin), por fora da
// RLS, como a migration de backfill faria; chamar de novo devolve o mesmo id.
func ProjetoPadrao(t *testing.T, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	admin := Admin(t)
	novo, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	// O SELECT do UNION não enxerga a linha que o CTE insere: sai uma linha só,
	// a nova ou a que já existia.
	var projeto uuid.UUID
	err = admin.QueryRow(ctx, `
		WITH novo AS (
		    INSERT INTO projects (id, name, titular_id, is_default, created_at)
		    VALUES ($1, 'Projeto padrão', $2, true, now())
		    ON CONFLICT (titular_id) WHERE is_default DO NOTHING
		    RETURNING id)
		SELECT id FROM novo
		UNION ALL
		SELECT id FROM projects WHERE titular_id = $2 AND is_default`, novo, userID).Scan(&projeto)
	if err != nil {
		t.Fatalf("criar o Projeto padrão: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES ($1, $2, 'owner', now())
		ON CONFLICT (project_id, user_id) DO NOTHING`, projeto, userID); err != nil {
		t.Fatalf("vincular o titular ao Projeto padrão: %v", err)
	}
	return projeto
}

// NovoProjeto cria um projeto que não é o padrão, com titular como owner, e
// devolve o id. Grava como superusuário (Admin), por fora da RLS: o escopo de
// projeto que cria projetos é de outra tarefa.
func NovoProjeto(t *testing.T, titular uuid.UUID, nome string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	admin := Admin(t)
	projeto, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO projects (id, name, titular_id, is_default, created_at)
		VALUES ($1, $2, $3, false, now())`, projeto, nome, titular); err != nil {
		t.Fatalf("criar o projeto %q: %v", nome, err)
	}
	Membro(t, projeto, titular, "owner")
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
