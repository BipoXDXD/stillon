package storage_test

import (
	"context"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/storage/testdb"
)

// TestMigrationsApplyAndSchemaIsUsable prova três coisas de uma vez: o
// testcontainers funciona nesta máquina, as migrations aplicam, e o schema
// resultante aceita as operações que o resto do plano vai depender.
func TestMigrationsApplyAndSchemaIsUsable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	for _, table := range []string{"users", "sessions", "audit_log"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			table).Scan(&exists)
		if err != nil {
			t.Fatalf("consultar tabela %s: %v", table, err)
		}
		if !exists {
			t.Errorf("tabela %s não existe depois das migrations", table)
		}
	}
}

// TestEmailIsCaseInsensitive verifica que a coluna citext faz o que se espera
// dela: dois cadastros que diferem só em caixa colidem.
func TestEmailIsCaseInsensitive(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	insert := `INSERT INTO users (id, email, password_hash) VALUES (gen_random_uuid(), $1, 'x')`

	if _, err := pool.Exec(ctx, insert, "Pessoa@Exemplo.com"); err != nil {
		t.Fatalf("primeiro insert deveria passar: %v", err)
	}
	if _, err := pool.Exec(ctx, insert, "pessoa@exemplo.com"); err == nil {
		t.Error("esperava violação de unicidade para e-mail em caixa diferente")
	}
}

// TestSnapshotIsolatesTests confirma que cada teste recebe banco limpo. Se
// falhar, todos os testes de integração seguintes são não confiáveis.
func TestSnapshotIsolatesTests(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("banco começou com %d usuários, queria 0 — o isolamento quebrou", count)
	}
}
