package account_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

const senha = "uma-senha-bem-comprida-e-boa"

func novo(t *testing.T) (*account.Service, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	return account.NewService(pool, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))), pool
}

// Dois `vigilume user create` ao mesmo tempo não criam dois donos (D9 do Plano
// 5). O gancho segura cada criação entre a contagem e a inserção até as duas
// chegarem ali, ou por meio segundo: com a trava, a segunda não chega à
// contagem enquanto a primeira não comitar, e espera o meio segundo sozinha;
// sem a trava, as duas contam zero e as duas inserem.
func TestConcurrentCreateMakesOneUser(t *testing.T) {
	svc, pool := novo(t)
	var chegaram sync.WaitGroup
	chegaram.Add(2)
	todas := make(chan struct{})
	go func() { chegaram.Wait(); close(todas) }()
	svc.SetDepoisDeContar(func() {
		chegaram.Done()
		select {
		case <-todas:
		case <-time.After(500 * time.Millisecond):
		}
	})

	erros := make([]error, 2)
	var wg sync.WaitGroup
	for i, email := range []string{"a@exemplo.com", "b@exemplo.com"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, erros[i] = svc.CreateUser(context.Background(), email, senha)
		}()
	}
	wg.Wait()

	var ok, recusadas int
	for _, err := range erros {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, account.ErrUserExists):
			recusadas++
		default:
			t.Fatalf("erro inesperado: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || recusadas != 1 || n != 1 {
		t.Errorf("%d criações, %d recusas, %d usuários; queria 1, 1 e 1", ok, recusadas, n)
	}
}

func TestValidateEmail(t *testing.T) {
	bons := map[string]string{
		"dono@exemplo.com":     "dono@exemplo.com",
		"  Dono@Exemplo.com\t": "Dono@Exemplo.com",
	}
	for entrada, queria := range bons {
		if got, err := account.ValidateEmail(entrada); err != nil || got != queria {
			t.Errorf("ValidateEmail(%q) = %q, %v; queria %q", entrada, got, err, queria)
		}
	}
	for _, ruim := range []string{"", "dono", "Dono <dono@exemplo.com>", "a@b.com, c@d.com",
		"dono@" + string(make([]byte, 250))} {
		if _, err := account.ValidateEmail(ruim); !errors.Is(err, account.ErrInvalidEmail) {
			t.Errorf("ValidateEmail(%q) aceitou", ruim)
		}
	}
}
