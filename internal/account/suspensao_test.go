package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const emailDoDono = "dono@exemplo.com"

func suspensaEm(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) *time.Time {
	t.Helper()
	var em *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT suspended_at FROM users WHERE id = $1`, user).Scan(&em); err != nil {
		t.Fatal(err)
	}
	return em
}

// Suspender marca suspended_at, encerra TODAS as famílias vivas (aqui, a do
// cenário e uma segunda) e audita user.suspended, numa transação.
func TestSuspendRevokesSessionsAndAudits(t *testing.T) {
	c := montar(t)
	familiaPreexistente(t, c, c.user)
	if n := familiasVivas(t, c.pool, c.user); n != 2 {
		t.Fatalf("o cenário tem %d famílias vivas, queria 2", n)
	}

	revogadas, err := c.svc.Suspend(context.Background(), emailDoDono)
	if err != nil {
		t.Fatal(err)
	}
	if revogadas != 2 {
		t.Errorf("Suspend devolveu %d revogadas, queria 2", revogadas)
	}
	if n := familiasVivas(t, c.pool, c.user); n != 0 {
		t.Errorf("%d famílias continuam vivas depois da suspensão", n)
	}
	if n := eventos(t, c.pool, c.user, "user.suspended"); n != 1 {
		t.Errorf("%d auditorias user.suspended, queria 1", n)
	}
	if em := suspensaEm(t, c.pool, c.user); em == nil || !em.Equal(c.clk.Now()) {
		t.Errorf("suspended_at = %v, queria %v", em, c.clk.Now())
	}
}

// Suspender de novo não move o instante: vale o da primeira suspensão.
func TestSuspendKeepsFirstTimestamp(t *testing.T) {
	c := montar(t)
	primeira := c.clk.Now()
	if _, err := c.svc.Suspend(context.Background(), emailDoDono); err != nil {
		t.Fatal(err)
	}
	c.clk.Advance(time.Hour)
	if _, err := c.svc.Suspend(context.Background(), emailDoDono); err != nil {
		t.Fatal(err)
	}
	if em := suspensaEm(t, c.pool, c.user); em == nil || !em.Equal(primeira) {
		t.Errorf("suspended_at = %v, queria o da primeira suspensão (%v)", em, primeira)
	}
}

// E-mail sem conta é ErrUserNotFound, nas duas direções.
func TestSuspendUnknownEmailIsNotFound(t *testing.T) {
	c := montar(t)
	if _, err := c.svc.Suspend(context.Background(), "ninguem@exemplo.com"); !errors.Is(err, account.ErrUserNotFound) {
		t.Errorf("Suspend de e-mail sem conta: %v, queria ErrUserNotFound", err)
	}
	if err := c.svc.Unsuspend(context.Background(), "ninguem@exemplo.com"); !errors.Is(err, account.ErrUserNotFound) {
		t.Errorf("Unsuspend de e-mail sem conta: %v, queria ErrUserNotFound", err)
	}
}

// O operador não se suspende: a recusa não marca nada, não encerra a sessão
// dele e não audita.
func TestOperatorCannotBeSuspended(t *testing.T) {
	c := montar(t)
	testdb.Operador(t, c.user)

	revogadas, err := c.svc.Suspend(context.Background(), emailDoDono)
	if !errors.Is(err, account.ErrOperatorNotSuspendable) {
		t.Fatalf("Suspend do operador: %v, queria ErrOperatorNotSuspendable", err)
	}
	if revogadas != 0 {
		t.Errorf("a recusa contou %d revogadas", revogadas)
	}
	if em := suspensaEm(t, c.pool, c.user); em != nil {
		t.Errorf("o operador ficou com suspended_at = %v", em)
	}
	if n := familiasVivas(t, c.pool, c.user); n != 1 {
		t.Errorf("o operador ficou com %d famílias vivas, queria 1", n)
	}
	if n := eventos(t, c.pool, c.user, "user.suspended"); n != 0 {
		t.Errorf("a recusa auditou %d user.suspended", n)
	}
}

// Reativar limpa suspended_at e audita user.unsuspended. As sessões
// encerradas pela suspensão não voltam.
func TestUnsuspendClearsAndAudits(t *testing.T) {
	c := montar(t)
	if _, err := c.svc.Suspend(context.Background(), emailDoDono); err != nil {
		t.Fatal(err)
	}
	if err := c.svc.Unsuspend(context.Background(), emailDoDono); err != nil {
		t.Fatal(err)
	}
	if em := suspensaEm(t, c.pool, c.user); em != nil {
		t.Errorf("suspended_at = %v depois de reativar, queria nulo", em)
	}
	if n := eventos(t, c.pool, c.user, "user.unsuspended"); n != 1 {
		t.Errorf("%d auditorias user.unsuspended, queria 1", n)
	}
	if n := familiasVivas(t, c.pool, c.user); n != 0 {
		t.Errorf("%d famílias voltaram com a reativação", n)
	}
}
