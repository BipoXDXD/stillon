package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

// Se este teste falhar, qualquer teste de RLS do repositório é suspeito: um
// pool de superusuário atravessa as políticas, e os testes de isolamento
// passariam pelo WHERE da query, nunca pela política.
func TestTestPoolDoesNotBypassRLS(t *testing.T) {
	pool := testdb.New(t)
	if err := storage.CheckRole(context.Background(), pool); err != nil {
		t.Fatalf("o pool de teste ignora RLS: %v", err)
	}
}

func TestCheckRoleRefusesSuperuser(t *testing.T) {
	ctx := context.Background()
	pool, err := storage.NewPool(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	err = storage.CheckRole(ctx, pool)
	if !errors.Is(err, storage.ErrRoleBypassesRLS) {
		t.Errorf("CheckRole com superusuário = %v, queria ErrRoleBypassesRLS", err)
	}
}
