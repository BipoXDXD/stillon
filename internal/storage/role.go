package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRoleBypassesRLS = errors.New("o papel do banco ignora row-level security")

// CheckRole recusa subir com um papel que ignora RLS.
//
// Superusuário e BYPASSRLS atravessam as políticas mesmo com FORCE ROW LEVEL
// SECURITY. Rodar a aplicação com um deles não produz erro nenhum: só
// desliga, em silêncio, a segunda barreira de isolamento da spec §5.5. Por
// isso a checagem é no boot, e é erro, não aviso (D8).
func CheckRole(ctx context.Context, pool *pgxpool.Pool) error {
	var super, bypass bool
	err := pool.QueryRow(ctx,
		`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		Scan(&super, &bypass)
	if err != nil {
		return fmt.Errorf("consultar papel do banco: %w", err)
	}
	if super || bypass {
		return fmt.Errorf("%w (rolsuper=%v, rolbypassrls=%v): conecte com um papel comum que seja dono das tabelas",
			ErrRoleBypassesRLS, super, bypass)
	}
	return nil
}
