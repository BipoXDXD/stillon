package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// contarProjetoPadrao conta, pelo superusuário (fora da RLS), os Projetos
// padrão do titular e os vínculos de owner dele.
func contarProjetoPadrao(t *testing.T, titular uuid.UUID) (projetos, owners int) {
	t.Helper()
	ctx := context.Background()
	admin := testdb.Admin(t)
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM projects WHERE titular_id = $1 AND is_default`, titular).Scan(&projetos); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM project_members WHERE user_id = $1 AND role = 'owner'`, titular).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	return projetos, owners
}

// Duas chamadas no escopo do usuário dão o mesmo projeto: uma linha em
// projects (is_default) e uma em project_members (owner).
func TestCriarProjetoPadraoNaTxEIdempotente(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)

	ids := make([]uuid.UUID, 2)
	for i := range ids {
		err := storage.InUserTx(ctx, pool, dono, func(q *db.Queries) error {
			var err error
			ids[i], err = storage.CriarProjetoPadraoNaTx(ctx, q, dono, agora)
			return err
		})
		if err != nil {
			t.Fatalf("chamada %d: %v", i+1, err)
		}
	}
	if ids[0] == uuid.Nil || ids[0] != ids[1] {
		t.Fatalf("ids = %v e %v; queria o mesmo, não nulo", ids[0], ids[1])
	}
	projetos, owners := contarProjetoPadrao(t, dono)
	if projetos != 1 || owners != 1 {
		t.Errorf("%d Projetos padrão e %d owners; queria 1 e 1", projetos, owners)
	}
	var vinculado uuid.UUID
	if err := testdb.Admin(t).QueryRow(ctx,
		`SELECT project_id FROM project_members WHERE user_id = $1 AND role = 'owner'`, dono).Scan(&vinculado); err != nil {
		t.Fatal(err)
	}
	if vinculado != ids[0] {
		t.Errorf("owner vinculado a %v, queria %v", vinculado, ids[0])
	}
}

// Sem o escopo do usuário, a RLS recusa e nada fica gravado.
func TestCriarProjetoPadraoNaTxSemEscopoFalha(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.CriarProjetoPadraoNaTx(ctx, db.New(tx), dono, agora); err == nil {
		t.Error("sem escopo, CriarProjetoPadraoNaTx não devolveu erro")
	}
	// O commit de uma transação abortada vira rollback; se não abortou, o
	// commit é que gravaria o que vazou.
	_ = tx.Commit(ctx)

	projetos, owners := contarProjetoPadrao(t, dono)
	if projetos != 0 || owners != 0 {
		t.Errorf("%d Projetos padrão e %d owners sem escopo; queria 0 e 0", projetos, owners)
	}
}
