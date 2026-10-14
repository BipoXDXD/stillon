package account_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const senha = "uma-senha-bem-comprida-e-boa"

// O Argon2id custa ~100 ms e disputa o semáforo global de derivações: nos
// testes em que ele não é o assunto, o serviço usa este par barato.
const prefixoBarato = "barato$"

func hashBarato(_ context.Context, plain string) (string, error) {
	return prefixoBarato + plain, nil
}

func verificarBarato(_ context.Context, encoded, plain string) (ok, needsRehash bool, err error) {
	return encoded == prefixoBarato+plain, false, nil
}

func comHasherBarato(svc *account.Service) *account.Service {
	svc.SetPasswordHasher(hashBarato)
	svc.SetPasswordVerifier(verificarBarato)
	return svc
}

func novo(t *testing.T) (*account.Service, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	svc := account.NewService(pool, nil, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)), nil)
	return comHasherBarato(svc), pool
}

// Dois `stillon user create` ao mesmo tempo não criam dois donos (D9 do Plano
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

// A borda do limite de 254 bytes (RFC 5321 com a errata 1690), com endereços
// que o net/mail aceita: 254 passa, 255 não. Os dois são o mesmo endereço a
// menos de um byte, então a recusa só pode vir do limite.
func TestValidateEmailLengthEdge(t *testing.T) {
	endereco := func(n int) string {
		// 64 + "@" + 63 + "." + 63 + "." + resto = n bytes.
		e := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." +
			strings.Repeat("d", n-64-1-63-1-63-1)
		if len(e) != n {
			t.Fatalf("endereço de %d bytes, queria %d", len(e), n)
		}
		return e
	}
	if got, err := account.ValidateEmail(endereco(254)); err != nil || got != endereco(254) {
		t.Errorf("254 bytes: %q, %v; queria aceito", got, err)
	}
	if _, err := account.ValidateEmail(endereco(255)); !errors.Is(err, account.ErrInvalidEmail) {
		t.Errorf("255 bytes: erro = %v, queria ErrInvalidEmail", err)
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
	hash  string    // o hash gravado de senha
	clk   *clock.Fake
	totp  *auth.TOTP
}

func montar(t *testing.T) cenario {
	t.Helper()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	box, err := crypto.NewBox([]byte("chave-mestra-de-teste-32-bytes!!"))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hashBarato(context.Background(), senha)
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, 'dono@exemplo.com', $2)`, user, hash); err != nil {
		t.Fatal(err)
	}
	mgr := auth.NewManager(auth.NewPGStore(pool), clk, ipKey)
	_, s, err := mgr.Issue(context.Background(), user, auth.PasswordStamp(hash), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	segundoFator := auth.NewTOTP(pool, box, "StillOn")
	svc := comHasherBarato(account.NewService(pool, segundoFator, clk, ipKey))
	return cenario{svc: svc, pool: pool, user: user, atual: s.FamilyID, hash: hash, clk: clk, totp: segundoFator}
}

func chaveDoUsuario(user uuid.UUID) string {
	return "hashtextextended('stillon.sessions:' || '" + user.String() + "', 0)"
}

// segurarTrava toma, numa conexão do teste, a mesma advisory lock que as
// mutações de sessão do usuário tomam (D1). Devolve a função que a solta.
//
// Soltar é idempotente, e o t.Cleanup solta se o teste morrer antes (um
// t.Fatal entre segurar e soltar): sem isso, a conexão continuaria com a
// trava, a goroutine do teste esperaria nela para sempre, e o fechamento do
// pool esperaria as duas até o timeout do go test.
func segurarTrava(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) func() {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var umaVez sync.Once
	soltar := func() {
		umaVez.Do(func() {
			if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(`+chaveDoUsuario(user)+`)`); err != nil {
				t.Error(err)
			}
			conn.Release()
		})
	}
	t.Cleanup(soltar)
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(`+chaveDoUsuario(user)+`)`); err != nil {
		t.Fatal(err)
	}
	return soltar
}

// esperarNaTravaDoUsuario espera a operação que devolve em fim aparecer
// esperando a advisory lock de sessões DESTE usuário. Devolve false, com o
// erro dela, se a operação terminar antes: terminar sem esperar é o defeito que
// os testes de trava procuram.
func esperarNaTravaDoUsuario(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, fim <-chan error) (esperou bool, errFim error) {
	t.Helper()
	// Uma advisory lock de chave bigint aparece em pg_locks partida em duas
	// metades de 32 bits: classid (a alta) e objid (a baixa), com objsubid 1.
	k := chaveDoUsuario(user)
	consulta := `SELECT count(*) FROM pg_locks
	  WHERE locktype = 'advisory' AND NOT granted AND objsubid = 1
	    AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
	    AND classid = ((` + k + ` >> 32) & 4294967295)::oid
	    AND objid = (` + k + ` & 4294967295)::oid`
	return esperarPresa(t, fim, func(ctx context.Context) bool {
		var n int
		if err := pool.QueryRow(ctx, consulta).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	})
}

// intervaloDaConsulta é o passo do polling, não um prazo: a espera só acaba
// por um dos dois eventos ou pelo t.Context().
const intervaloDaConsulta = 10 * time.Millisecond

// esperarPresa é a corrida entre dois eventos: a operação terminar (fim) ou
// presa dizer que ela está esperando uma trava. Não tem prazo próprio, porque
// prazo de parede falha à toa com a máquina carregada; o limite é o
// t.Context() (na prática, o -timeout do go test).
func esperarPresa(t *testing.T, fim <-chan error, presa func(context.Context) bool) (esperou bool, errFim error) {
	t.Helper()
	ctx := t.Context()
	passo := time.NewTicker(intervaloDaConsulta)
	defer passo.Stop()
	for {
		select {
		case err := <-fim:
			return false, err
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-passo.C:
			if presa(ctx) {
				return true, nil
			}
		}
	}
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

	if esperou, err := esperarNaTravaDoUsuario(t, c.pool, c.user, fim); !esperou {
		t.Fatalf("a troca não esperou a trava do usuário (terminou com %v)", err)
	}
	soltar()
	if err := <-fim; err != nil {
		t.Fatalf("a troca falhou depois de soltar a trava: %v", err)
	}
}

// Sabotagem: consumir o segundo fator antes do hash novo. O hasher enche o
// semáforo real exatamente no segundo Argon2, sem depender de corrida/sleep.
func TestChangePasswordKeepsRecoveryCodeWhenTheNewHashIsRateLimited(t *testing.T) {
	c := montar(t)
	ctx := t.Context()
	uri, err := c.totp.Enroll(ctx, c.user, "dono@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(key.Secret(), c.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	codes, err := c.totp.Confirm(ctx, c.user, code, c.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("%d códigos de recuperação, queria 10", len(codes))
	}

	limiteAnterior := auth.Argon2MaxConcurrent()
	auth.SetArgon2MaxConcurrent(1)
	t.Cleanup(func() { auth.SetArgon2MaxConcurrent(limiteAnterior) })
	c.svc.SetPasswordHasher(func(ctx context.Context, password string) (string, error) {
		soltar, err := auth.ReserveArgon2(ctx)
		if err != nil {
			return "", err
		}
		defer soltar()
		return auth.HashPassword(ctx, password)
	})
	err = c.svc.ChangePassword(ctx, c.user, c.atual,
		account.ChangePasswordInput{Current: senha, New: "a-senha-nova-bem-comprida", TOTPCode: codes[0]},
		auth.Meta{IP: "203.0.113.9"})
	if !errors.Is(err, auth.ErrArgon2Busy) {
		t.Fatalf("troca = %v, queria o erro que a API responde como 429", err)
	}
	var usados int
	if err := c.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = 'totp.recovery_used'`, c.user).Scan(&usados); err != nil {
		t.Fatal(err)
	}
	if usados != 0 {
		t.Errorf("%d consumos auditados depois do 429, queria nenhum", usados)
	}
	if err := c.totp.Verify(ctx, c.user, codes[0], c.clk.Now()); err != nil {
		t.Errorf("o código de recuperação foi gasto pelo 429: %v", err)
	}
	var hash string
	if err := c.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, c.user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != c.hash {
		t.Error("a senha atual deixou de valer depois do 429: o hash gravado mudou")
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

	if esperou, err := esperarNaTravaDoUsuario(t, c.pool, c.user, fim); !esperou {
		t.Fatalf("a redefinição não esperou a trava do usuário (terminou com %v)", err)
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
	if esperou, err := esperarNaTravaDoUsuario(t, c.pool, c.user, fim); !esperou {
		t.Fatalf("a troca não chegou à trava (terminou com %v)", err)
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
	if hash != c.hash {
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
	if esperou, err := esperarNaTravaDoUsuario(t, c.pool, c.user, fim); !esperou {
		t.Fatalf("a troca não chegou à trava (terminou com %v)", err)
	}
	outra, err := hashBarato(context.Background(), "a-outra-troca-que-ganhou")
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

// Encerrar uma sessão também toma a trava do usuário (D1): é o que impede o
// sucessor de uma rotação em curso de escapar da revogação.
func TestRevokeSessionTakesTheUserLock(t *testing.T) {
	c := montar(t)
	soltar := segurarTrava(t, c.pool, c.user)
	fim := make(chan error, 1)
	go func() {
		fim <- c.svc.RevokeSession(context.Background(), c.user, c.atual, auth.Meta{IP: "203.0.113.9"})
	}()

	if esperou, err := esperarNaTravaDoUsuario(t, c.pool, c.user, fim); !esperou {
		t.Fatalf("o encerramento não esperou a trava do usuário (terminou com %v)", err)
	}
	soltar()
	if err := <-fim; err != nil {
		t.Fatalf("o encerramento falhou depois de soltar a trava: %v", err)
	}
}

// segredoPendente diz se há segredo TOTP gravado e se está confirmado.
func segredoPendente(t *testing.T, c cenario) (temSegredo, confirmado bool) {
	t.Helper()
	var sealed []byte
	var at *time.Time
	if err := c.pool.QueryRow(context.Background(),
		`SELECT totp_secret_enc, totp_confirmed_at FROM users WHERE id = $1`, c.user).Scan(&sealed, &at); err != nil {
		t.Fatal(err)
	}
	return len(sealed) > 0, at != nil
}

// cadastrarTOTP cadastra e, se pedido, confirma o segundo fator do cenário.
// Devolve o segredo.
func cadastrarTOTP(t *testing.T, c cenario, confirmar bool) string {
	t.Helper()
	ctx := context.Background()
	uri, err := c.totp.Enroll(ctx, c.user, "dono@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	key, err := otp.NewKeyFromURL(uri)
	if err != nil {
		t.Fatal(err)
	}
	if confirmar {
		code, err := totp.GenerateCode(key.Secret(), c.clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.totp.Confirm(ctx, c.user, code, c.clk.Now()); err != nil {
			t.Fatal(err)
		}
		c.clk.Advance(30 * time.Second) // o timestep da confirmação já foi consumido
	}
	return key.Secret()
}

// A troca de senha e o set-password apagam um cadastro de 2FA pendente (não
// confirmado), na mesma transação que troca o hash: quem o iniciou pode ser o
// invasor que a troca está expulsando. O 2FA confirmado fica (D10).
func TestPasswordChangeDropsAPendingTOTPEnrollment(t *testing.T) {
	redefinir := map[string]func(c cenario, segredo string) error{
		"troca de senha": func(c cenario, segredo string) error {
			in := account.ChangePasswordInput{Current: senha, New: "a-senha-nova-bem-comprida"}
			if segredo != "" {
				code, err := totp.GenerateCode(segredo, c.clk.Now())
				if err != nil {
					return err
				}
				in.TOTPCode = code
			}
			return c.svc.ChangePassword(context.Background(), c.user, c.atual, in, auth.Meta{IP: "203.0.113.9"})
		},
		"set-password": func(c cenario, _ string) error {
			_, err := c.svc.ResetPassword(context.Background(), "dono@exemplo.com", "a-senha-redefinida-pelo-cli")
			return err
		},
	}
	for nome, fn := range redefinir {
		t.Run(nome+", pendente", func(t *testing.T) {
			c := montar(t)
			cadastrarTOTP(t, c, false)
			if err := fn(c, ""); err != nil {
				t.Fatal(err)
			}
			if tem, _ := segredoPendente(t, c); tem {
				t.Error("o segredo TOTP pendente sobreviveu à troca de senha")
			}
		})
		t.Run(nome+", confirmado", func(t *testing.T) {
			c := montar(t)
			segredo := cadastrarTOTP(t, c, true)
			if err := fn(c, segredo); err != nil {
				t.Fatal(err)
			}
			if tem, confirmado := segredoPendente(t, c); !tem || !confirmado {
				t.Errorf("o 2FA confirmado caiu com a troca de senha: segredo = %v, confirmado = %v", tem, confirmado)
			}
		})
	}
}

// A confirmação que leu o segredo pendente antes de a troca de senha apagá-lo
// não pode ligar o 2FA depois: ficaria confirmado SEM segredo, e o dono nunca
// mais entraria (todo login pediria um código que não existe). A ordem é
// imposta pelo banco: a linha do usuário travada pelo teste; a troca espera
// no UPDATE do hash; a confirmação, que já validou o código com o segredo
// lido, espera no UPDATE dela, atrás da troca.
func TestPendingTOTPConfirmLosesToAPasswordChange(t *testing.T) {
	c := montar(t)
	segredo := cadastrarTOTP(t, c, false)
	ctx := context.Background()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var umaVez sync.Once
	soltar := func() { umaVez.Do(func() { _ = tx.Rollback(ctx) }) }
	t.Cleanup(soltar)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, c.user); err != nil {
		t.Fatal(err)
	}
	var donoDaLinha int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&donoDaLinha); err != nil {
		t.Fatal(err)
	}

	fimTroca := make(chan error, 1)
	go func() { fimTroca <- trocar(c) }()
	if esperou, err := esperarPresosAtras(t, c.pool, donoDaLinha, 1, fimTroca); !esperou {
		t.Fatalf("a troca não chegou ao UPDATE do hash (terminou com %v)", err)
	}
	code, err := totp.GenerateCode(segredo, c.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	fimConfirm := make(chan error, 1)
	go func() {
		_, err := c.totp.Confirm(ctx, c.user, code, c.clk.Now())
		fimConfirm <- err
	}()
	if esperou, err := esperarPresosAtras(t, c.pool, donoDaLinha, 2, fimConfirm); !esperou {
		t.Fatalf("a confirmação não chegou ao UPDATE dela (terminou com %v)", err)
	}
	soltar()

	if err := <-fimTroca; err != nil {
		t.Fatalf("a troca falhou: %v", err)
	}
	if err := <-fimConfirm; !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Errorf("confirmação = %v, queria ErrTOTPNotEnrolled", err)
	}
	if tem, confirmado := segredoPendente(t, c); confirmado || tem {
		t.Errorf("segredo = %v, confirmado = %v; queria nenhum dos dois", tem, confirmado)
	}
}

// esperarPresosAtras espera n backends presos, direta ou indiretamente, atrás
// da transação do backend dono (a fila da linha travada pelo teste). Devolve
// false, com o erro dela, se a operação de fim terminar antes de a fila chegar
// a n.
func esperarPresosAtras(t *testing.T, pool *pgxpool.Pool, dono, n int, fim <-chan error) (esperou bool, errFim error) {
	t.Helper()
	return esperarPresa(t, fim, func(ctx context.Context) bool {
		var m int
		if err := pool.QueryRow(ctx,
			`WITH RECURSIVE presos(pid) AS (
			     SELECT pid FROM pg_stat_activity WHERE $1::int = ANY (pg_blocking_pids(pid))
			     UNION
			     SELECT a.pid FROM pg_stat_activity a JOIN presos p ON p.pid = ANY (pg_blocking_pids(a.pid))
			 )
			 SELECT count(*) FROM presos`, dono).Scan(&m); err != nil {
			t.Fatal(err)
		}
		return m >= n
	})
}

// familiaRenovada abre uma família e a renova duas vezes: três linhas, uma
// viva.
func familiaRenovada(t *testing.T, c cenario) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var hash string
	if err := c.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, c.user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	mgr := auth.NewManager(auth.NewPGStore(c.pool), c.clk, ipKey)
	tok, s, err := mgr.Issue(ctx, c.user, auth.PasswordStamp(hash), auth.Meta{IP: "203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if tok, _, err = mgr.Rotate(ctx, tok, auth.Meta{IP: "203.0.113.9"}); err != nil {
			t.Fatal(err)
		}
	}
	return s.FamilyID
}

func linhasRevogadas(t *testing.T, c cenario, familia uuid.UUID) (revogadas, total int) {
	t.Helper()
	if err := c.pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE revoked_at IS NOT NULL), count(*) FROM sessions WHERE family_id = $1`,
		familia).Scan(&revogadas, &total); err != nil {
		t.Fatal(err)
	}
	return revogadas, total
}

// DELETE /sessions/{id} e a troca de senha marcam só a linha viva da família
// (Task 11): as rotacionadas ficam para a retenção, que as apaga numa
// transação longa, e disputar as mesmas linhas em outra ordem arriscava
// deadlock. (O set-password tem a mesma asserção no teste do CLI.)
func TestRevocationsMarkOnlyTheLiveRow(t *testing.T) {
	c := montar(t)
	encerrada := familiaRenovada(t, c)
	if err := c.svc.RevokeSession(context.Background(), c.user, encerrada, auth.Meta{IP: "203.0.113.9"}); err != nil {
		t.Fatal(err)
	}
	if r, n := linhasRevogadas(t, c, encerrada); r != 1 || n != 3 {
		t.Errorf("DELETE /sessions: %d de %d linhas revogadas, queria 1 de 3", r, n)
	}

	outra := familiaRenovada(t, c)
	if err := trocar(c); err != nil {
		t.Fatal(err)
	}
	if r, n := linhasRevogadas(t, c, outra); r != 1 || n != 3 {
		t.Errorf("troca de senha: %d de %d linhas revogadas, queria 1 de 3", r, n)
	}
}

func hashGravado(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var hash string
	if err := pool.QueryRow(context.Background(),
		`SELECT password_hash FROM users WHERE email = 'dono@exemplo.com'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

// O par injetado é o único caminho da senha no serviço: CreateUser e
// ResetPassword derivam com ele, e ChangePassword verifica e deriva com ele.
// Se algum caminho chamar o auth direto, a verificação recusa o hash barato
// ou o hash gravado sai em Argon2id.
func TestServiceUsesTheInjectedHasher(t *testing.T) {
	t.Run("CreateUser", func(t *testing.T) {
		svc, pool := novo(t)
		if _, err := svc.CreateUser(context.Background(), "dono@exemplo.com", senha); err != nil {
			t.Fatal(err)
		}
		if h := hashGravado(t, pool); h != prefixoBarato+senha {
			t.Errorf("hash gravado = %q, queria o do hasher injetado", h)
		}
	})
	t.Run("ResetPassword", func(t *testing.T) {
		c := montar(t)
		if _, err := c.svc.ResetPassword(context.Background(), "dono@exemplo.com", "a-senha-redefinida-pelo-cli"); err != nil {
			t.Fatal(err)
		}
		if h := hashGravado(t, c.pool); h != prefixoBarato+"a-senha-redefinida-pelo-cli" {
			t.Errorf("hash gravado = %q, queria o do hasher injetado", h)
		}
	})
	t.Run("ChangePassword", func(t *testing.T) {
		c := montar(t)
		if err := trocar(c); err != nil {
			t.Fatalf("a troca não conferiu a senha atual pelo verificador injetado: %v", err)
		}
		if h := hashGravado(t, c.pool); h != prefixoBarato+"a-senha-nova-bem-comprida" {
			t.Errorf("hash gravado = %q, queria o do hasher injetado", h)
		}
	})
}

// Sem troca, o serviço é o do composition root: deriva com Argon2id, e o hash
// gravado confere pelo verificador de produção.
func TestNewServiceDerivesWithArgon2id(t *testing.T) {
	pool := testdb.New(t)
	svc := account.NewService(pool, nil, clock.NewFake(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)), nil)
	ctx := context.Background()
	if _, err := svc.CreateUser(ctx, "dono@exemplo.com", senha); err != nil {
		t.Fatal(err)
	}
	criado := hashGravado(t, pool)
	if _, err := svc.ResetPassword(ctx, "dono@exemplo.com", "a-senha-redefinida-pelo-cli"); err != nil {
		t.Fatal(err)
	}
	redefinido := hashGravado(t, pool)
	for nome, c := range map[string]struct{ hash, senha string }{
		"CreateUser":    {criado, senha},
		"ResetPassword": {redefinido, "a-senha-redefinida-pelo-cli"},
	} {
		if !strings.HasPrefix(c.hash, "$argon2id$") {
			t.Errorf("%s gravou %q, queria Argon2id", nome, c.hash)
		}
		if ok, _, err := auth.VerifyPassword(ctx, c.hash, c.senha); err != nil || !ok {
			t.Errorf("%s: o hash não confere com a senha: ok=%v, erro=%v", nome, ok, err)
		}
	}
}
