package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Beginner é o mínimo que uma transação com escopo precisa. *pgxpool.Pool e
// *pgxpool.Conn satisfazem; aceitar a conexão avulsa é o que permite provar
// o comportamento numa conexão REUSADA (TestScopeSurvivesConnectionReuse).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var ErrNoUser = errors.New("transação de usuário sem usuário")

// InUserTx roda fn numa transação em que a RLS só enxerga as linhas de userID.
//
// set_config(..., true) é o SET LOCAL: o valor morre no fim da transação e
// não vaza para o próximo uso da conexão no pool. Ver a migration 00004 para
// o motivo do NULLIF nas políticas.
func InUserTx(ctx context.Context, b Beginner, userID uuid.UUID, fn func(*db.Queries) error) error {
	if userID == uuid.Nil {
		// uuid.Nil nunca é usuário. Aceitá-lo produziria uma transação que
		// não enxerga nada, e o erro apareceria longe daqui, como "não
		// encontrado".
		return ErrNoUser
	}
	return inTx(ctx, b, `SELECT set_config('app.user_id', $1, true)`, userID.String(), fn)
}

// InProberTx roda fn com o escopo do sondador: lê e reagenda alvos de todos
// os usuários e grava checks, mas não cria nem apaga alvos (políticas
// targets_prober_*). É o ÚNICO lugar do código que liga esse escopo.
func InProberTx(ctx context.Context, b Beginner, fn func(*db.Queries) error) error {
	return inTx(ctx, b, `SELECT set_config('app.scope', $1, true)`, "prober", fn)
}

func inTx(ctx context.Context, b Beginner, setSQL, valor string, fn func(*db.Queries) error) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return fmt.Errorf("abrir transação: %w", err)
	}
	defer tx.Rollback(ctx) // no-op depois de Commit

	if _, err := tx.Exec(ctx, setSQL, valor); err != nil {
		return fmt.Errorf("definir escopo da RLS: %w", err)
	}
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
