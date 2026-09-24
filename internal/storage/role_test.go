package storage_test

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

// O superusuário do contêiner tem rolsuper E rolbypassrls, então o teste
// acima fica verde mesmo se CheckRole olhar só rolsuper. Este trava o outro
// ramo: um papel comum, mas com BYPASSRLS, também atravessa as políticas e
// tem de ser recusado.
func TestCheckRoleRefusesBypassRLSWithoutSuperuser(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)

	// Papéis são globais ao cluster e o Restore do testdb não os desfaz:
	// nome único e DROP ROLE no cleanup.
	cru := "vigilume_bypass_" + uuid.Must(uuid.NewV7()).String()[:8]
	nome := pgx.Identifier{cru}.Sanitize()
	const senha = "bypass"
	if _, err := admin.Exec(ctx, `CREATE ROLE `+nome+` LOGIN PASSWORD '`+senha+`' NOSUPERUSER BYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), testdb.AdminDSN(t))
		if err != nil {
			t.Errorf("conectar para apagar papel: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), `DROP ROLE IF EXISTS `+nome); err != nil {
			t.Errorf("apagar papel: %v", err)
		}
	})

	u, err := url.Parse(testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(cru, senha)
	pool, err := storage.NewPool(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Confere a premissa: sem ela, o teste provaria o ramo errado.
	var super, bypass bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || !bypass {
		t.Fatalf("papel de teste com rolsuper=%v, rolbypassrls=%v; queria false, true", super, bypass)
	}

	err = storage.CheckRole(ctx, pool)
	if !errors.Is(err, storage.ErrRoleBypassesRLS) {
		t.Errorf("CheckRole com papel NOSUPERUSER BYPASSRLS = %v, queria ErrRoleBypassesRLS", err)
	}
}
