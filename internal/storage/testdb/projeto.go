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
