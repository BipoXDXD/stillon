package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/auth"
)

// estadoDo2FA lê as colunas que o reset-2fa zera (Plano 9, §3.1), os códigos de
// recuperação e a marca do desafio, que ele NÃO mexe.
type estadoDo2FA struct {
	segredo, confirmado, timestep, pendente bool
	codigos                                 int
	marca                                   *time.Time
}

func lerEstadoDo2FA(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) estadoDo2FA {
	t.Helper()
	ctx := context.Background()
	var e estadoDo2FA
	if err := pool.QueryRow(ctx,
		`SELECT totp_secret_enc IS NOT NULL, totp_confirmed_at IS NOT NULL, totp_last_timestep IS NOT NULL,
		        totp_pending_secret_enc IS NOT NULL, totp_challenge_mark
		   FROM users WHERE id = $1`, user).
		Scan(&e.segredo, &e.confirmado, &e.timestep, &e.pendente, &e.marca); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM totp_recovery_codes WHERE user_id = $1`, user).Scan(&e.codigos); err != nil {
		t.Fatal(err)
	}
	return e
}

func codigoAgora(t *testing.T, c cenario, segredo string) string {
	t.Helper()
	code, err := totp.GenerateCode(segredo, c.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func familiasVivas(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE user_id = $1 AND used_at IS NULL AND revoked_at IS NULL`,
		user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func eventos(t *testing.T, pool *pgxpool.Pool, user uuid.UUID, acao string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE user_id = $1 AND action = $2`, user, acao).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Com o 2FA ligado (e uma troca pendente), o reset-2fa desliga tudo numa
// transação: as quatro colunas nulas, os códigos apagados, TODAS as sessões
// encerradas e a auditoria totp.reset_cli. A marca do desafio fica. Outro
// usuário não é tocado.
func TestResetTwoFactorTurnsItOff(t *testing.T) {
	c := montar(t)
	ctx := context.Background()
	segredo := cadastrarTOTP(t, c, true)
	if _, err := c.totp.StartRotation(ctx, c.user, "dono@exemplo.com", codigoAgora(t, c, segredo), c.clk.Now()); err != nil {
		t.Fatal(err)
	}
	marca := c.clk.Now().Add(-time.Minute)
	if _, err := c.pool.Exec(ctx, `UPDATE users SET totp_challenge_mark = $2 WHERE id = $1`, c.user, marca); err != nil {
		t.Fatal(err)
	}
	// Uma segunda família do dono, e um outro usuário com 2FA e sessão.
	mgr := auth.NewManager(auth.NewPGStore(c.pool), c.clk, ipKey)
	var hash string
	if err := c.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, c.user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Issue(ctx, c.user, auth.PasswordStamp(hash), auth.Meta{IP: "203.0.113.9"}); err != nil {
		t.Fatal(err)
	}
	outro := uuid.New()
	if _, err := c.pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, totp_secret_enc, totp_confirmed_at) VALUES ($1, 'outro@exemplo.com', 'x', '\x01', $2)`,
		outro, c.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.pool.Exec(ctx,
		`INSERT INTO totp_recovery_codes (id, user_id, code_hash) VALUES ($1, $2, '\x02')`, uuid.New(), outro); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Issue(ctx, outro, auth.PasswordStamp("x"), auth.Meta{IP: "203.0.113.9"}); err != nil {
		t.Fatal(err)
	}

	antes := lerEstadoDo2FA(t, c.pool, c.user)
	if !antes.segredo || !antes.confirmado || !antes.timestep || !antes.pendente || antes.codigos != 10 {
		t.Fatalf("o cenário não tem o 2FA completo: %+v", antes)
	}

	ligado, n, err := c.svc.ResetTwoFactor(ctx, " dono@exemplo.com ")
	if err != nil {
		t.Fatal(err)
	}
	if !ligado || n != 2 {
		t.Errorf("ResetTwoFactor devolveu ligado=%v, %d sessões; queria true, 2", ligado, n)
	}
	depois := lerEstadoDo2FA(t, c.pool, c.user)
	if depois.segredo || depois.confirmado || depois.timestep || depois.pendente {
		t.Errorf("sobrou estado do 2FA: %+v", depois)
	}
	if depois.codigos != 0 {
		t.Errorf("sobraram %d códigos de recuperação", depois.codigos)
	}
	if depois.marca == nil || !depois.marca.Equal(marca) {
		t.Errorf("a marca do desafio mudou: %v, queria %v", depois.marca, marca)
	}
	if v := familiasVivas(t, c.pool, c.user); v != 0 {
		t.Errorf("%d sessões do dono continuaram vivas", v)
	}
	if n := eventos(t, c.pool, c.user, "totp.reset_cli"); n != 1 {
		t.Errorf("%d eventos totp.reset_cli, queria 1", n)
	}

	o := lerEstadoDo2FA(t, c.pool, outro)
	if !o.segredo || !o.confirmado || o.codigos != 1 || familiasVivas(t, c.pool, outro) != 1 {
		t.Errorf("o reset-2fa mexeu em outro usuário: %+v, sessões vivas %d", o, familiasVivas(t, c.pool, outro))
	}
}

// Sem 2FA, nada muda: nenhuma sessão encerrada, nenhuma auditoria. Um cadastro
// iniciado e não confirmado também fica (não é 2FA ligado).
func TestResetTwoFactorWhenAlreadyOff(t *testing.T) {
	c := montar(t)
	ctx := context.Background()
	cadastrarTOTP(t, c, false)

	ligado, n, err := c.svc.ResetTwoFactor(ctx, "dono@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	if ligado || n != 0 {
		t.Errorf("ResetTwoFactor sem 2FA devolveu ligado=%v, %d sessões; queria false, 0", ligado, n)
	}
	if v := familiasVivas(t, c.pool, c.user); v != 1 {
		t.Errorf("%d sessões vivas, queria 1: o reset-2fa sem 2FA encerrou sessões", v)
	}
	if n := eventos(t, c.pool, c.user, "totp.reset_cli"); n != 0 {
		t.Errorf("%d eventos totp.reset_cli sem 2FA ligado", n)
	}
	if e := lerEstadoDo2FA(t, c.pool, c.user); !e.segredo {
		t.Error("o reset-2fa sem 2FA apagou o cadastro não confirmado")
	}
}

func TestResetTwoFactorUnknownEmail(t *testing.T) {
	c := montar(t)
	if _, _, err := c.svc.ResetTwoFactor(context.Background(), "ninguem@exemplo.com"); !errors.Is(err, account.ErrUserNotFound) {
		t.Errorf("e-mail desconhecido devolveu %v, queria ErrUserNotFound", err)
	}
	if v := familiasVivas(t, c.pool, c.user); v != 1 {
		t.Errorf("o e-mail desconhecido encerrou sessões de outro usuário (%d vivas)", v)
	}
}

// O reset-2fa toma a trava de sessões do usuário antes de revogar (D1 do
// Plano 5), como o set-password.
func TestResetTwoFactorTakesTheUserLock(t *testing.T) {
	c := montar(t)
	cadastrarTOTP(t, c, true)
	soltar := segurarTrava(t, c.pool, c.user)
	fim := make(chan error, 1)
	go func() {
		_, _, err := c.svc.ResetTwoFactor(context.Background(), "dono@exemplo.com")
		fim <- err
	}()

	if !esperarAlguemNaTrava(t, c.pool) {
		soltar()
		t.Fatalf("o reset-2fa não esperou a trava do usuário (terminou com %v)", <-fim)
	}
	soltar()
	if err := <-fim; err != nil {
		t.Fatalf("o reset-2fa falhou depois de soltar a trava: %v", err)
	}
}
