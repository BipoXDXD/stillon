package account_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/crypto"
	"github.com/BipoXDXD/vigilume/internal/storage/testdb"
)

const senha = "uma-senha-bem-comprida-e-boa"

func novo(t *testing.T) (*account.Service, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	return account.NewService(pool, nil, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)), nil), pool
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

var ipKey = []byte("chave-de-teste-com-32-bytes-ok!!")

// cenario é um usuário com senha e uma sessão viva (a "atual"), e o serviço
// completo, com segundo fator e chave de IP.
type cenario struct {
	svc   *account.Service
	pool  *pgxpool.Pool
	user  uuid.UUID
	atual uuid.UUID // família da sessão atual
}

func montar(t *testing.T) cenario {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	box, err := crypto.NewBox([]byte("chave-mestra-de-teste-32-bytes!!"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(senha)
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, 'dono@exemplo.com', $2)`, user, hash); err != nil {
		t.Fatal(err)
	}
	mgr := auth.NewManager(auth.NewPGStore(pool), clk, ipKey)
	_, s, err := mgr.Issue(context.Background(), user, auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	svc := account.NewService(pool, auth.NewTOTP(pool, box, "Vigilume"), clk, ipKey)
	return cenario{svc: svc, pool: pool, user: user, atual: s.FamilyID}
}

func chaveDoUsuario(user uuid.UUID) string {
	return "hashtextextended('vigilume.sessions:' || '" + user.String() + "', 0)"
}

// segurarTrava toma, numa conexão do teste, a mesma advisory lock que as
// mutações de sessão do usuário tomam (D1). Devolve a função que a solta.
func segurarTrava(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) func() {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+chaveDoUsuario(user)+`)`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+chaveDoUsuario(user)+`)`); err != nil {
			t.Error(err)
		}
		conn.Release()
	}
}

// esperarAlguemNaTrava espera um backend aparecer esperando uma advisory lock.
// Devolve false se ninguém aparecer: quem chama decide se isso é falha.
func esperarAlguemNaTrava(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	prazo := time.Now().Add(3 * time.Second)
	for time.Now().Before(prazo) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event = 'advisory'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func trocar(c cenario) error {
	return c.svc.ChangePassword(context.Background(), c.user, c.atual,
		account.ChangePasswordInput{Current: senha, New: "a-senha-nova-bem-comprida"}, auth.Meta{IP: "203.0.113.9"})
}

// A troca toma a trava do usuário antes de tocar em sessions (D1): com ela
// segura por fora, a troca espera; solta, termina.
func TestChangePasswordTakesTheUserLock(t *testing.T) {
	c := montar(t)
	soltar := segurarTrava(t, c.pool, c.user)
	fim := make(chan error, 1)
	go func() { fim <- trocar(c) }()

	if !esperarAlguemNaTrava(t, c.pool) {
		soltar()
		t.Fatalf("a troca não esperou a trava do usuário (terminou com %v)", <-fim)
	}
	soltar()
	if err := <-fim; err != nil {
		t.Fatalf("a troca falhou depois de soltar a trava: %v", err)
	}
}

// O set-password do CLI também toma a trava do usuário antes de revogar as
// famílias (D1, D10): sem ela, o sucessor de uma rotação em curso escaparia.
func TestResetPasswordTakesTheUserLock(t *testing.T) {
	c := montar(t)
	soltar := segurarTrava(t, c.pool, c.user)
	fim := make(chan error, 1)
	go func() {
		_, err := c.svc.ResetPassword(context.Background(), "dono@exemplo.com", "a-senha-redefinida-pelo-cli")
		fim <- err
	}()

	if !esperarAlguemNaTrava(t, c.pool) {
		soltar()
		t.Fatalf("a redefinição não esperou a trava do usuário (terminou com %v)", <-fim)
	}
	soltar()
	if err := <-fim; err != nil {
		t.Fatalf("a redefinição falhou depois de soltar a trava: %v", err)
	}
}

// A família atual pode cair entre a primeira checagem e a trava (um DELETE
// /sessions de outro aparelho). A troca confere de novo sob a trava, e aí nada
// muda: o sucesso sempre quer dizer "trocada e a atual continua viva".
func TestChangePasswordRechecksTheFamilyUnderTheLock(t *testing.T) {
	c := montar(t)
	soltar := segurarTrava(t, c.pool, c.user)
	fim := make(chan error, 1)
	go func() { fim <- trocar(c) }()
	if !esperarAlguemNaTrava(t, c.pool) {
		soltar()
		t.Fatal("a troca não chegou à trava")
	}
	// Revogada por fora enquanto a troca espera a trava.
	if _, err := c.pool.Exec(context.Background(),
		`UPDATE sessions SET revoked_at = now() WHERE family_id = $1`, c.atual); err != nil {
		t.Fatal(err)
	}
	soltar()

	if err := <-fim; !errors.Is(err, account.ErrSessionGone) {
		t.Fatalf("erro = %v, queria ErrSessionGone", err)
	}
	var hash string
	if err := c.pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, c.user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := auth.VerifyPassword(hash, senha); !ok {
		t.Error("a senha mudou mesmo com a sessão atual encerrada")
	}
}

// A senha mudou depois de verificada (outra troca concorrente): o CAS recusa,
// e a troca que perdeu não grava por cima.
func TestChangePasswordLosesToAConcurrentChange(t *testing.T) {
	c := montar(t)
	soltar := segurarTrava(t, c.pool, c.user)
	fim := make(chan error, 1)
	go func() { fim <- trocar(c) }()
	if !esperarAlguemNaTrava(t, c.pool) {
		soltar()
		t.Fatal("a troca não chegou à trava")
	}
	outra, err := auth.HashPassword("a-outra-troca-que-ganhou")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.pool.Exec(context.Background(), `UPDATE users SET password_hash = $2 WHERE id = $1`, c.user, outra); err != nil {
		t.Fatal(err)
	}
	soltar()

	if err := <-fim; !errors.Is(err, account.ErrInvalidCredentials) {
		t.Fatalf("erro = %v, queria ErrInvalidCredentials", err)
	}
	var hash string
	if err := c.pool.QueryRow(context.Background(), `SELECT password_hash FROM users WHERE id = $1`, c.user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != outra {
		t.Error("a troca que perdeu gravou por cima da que ganhou")
	}
}
