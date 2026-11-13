package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// apagaUsuarioNaLeitura apaga o usuário, por outra conexão, logo antes da
// primeira consulta a users que passa pelo pool vigiado. É o usuário que some
// entre a guarda da rota (token e reserva da prova da família, que não passam
// por este pool) e a leitura da conta pelo handler.
type apagaUsuarioNaLeitura struct {
	outro   *pgxpool.Pool
	usuario uuid.UUID
	feito   atomic.Bool
	erro    atomic.Value
}

func (a *apagaUsuarioNaLeitura) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "FROM users") && !a.feito.Swap(true) {
		// Com prazo: se a rota um dia travar a linha antes desta leitura, o
		// DELETE esperaria por ela para sempre. Assim o teste falha, não trava.
		prazo, cancelar := context.WithTimeout(context.Background(), prazoDoApagar)
		defer cancelar()
		if _, err := a.outro.Exec(prazo, `DELETE FROM users WHERE id = $1`, a.usuario); err != nil {
			a.erro.Store(err)
		}
	}
	return ctx
}

func (*apagaUsuarioNaLeitura) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// prazoDoApagar limita o DELETE do gatilho.
const prazoDoApagar = 5 * time.Second

type cenarioDoUsuarioSumido struct {
	h       *AuthHandlers
	pool    *pgxpool.Pool
	gatilho *apagaUsuarioNaLeitura
	clk     *clock.Fake
	usuario uuid.UUID
	familia uuid.UUID
}

func novoCenarioDoUsuarioSumido(t *testing.T) *cenarioDoUsuarioSumido {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	usuario, familia := uuid.New(), uuid.New()
	hash, err := auth.HashPassword(ctx, senhaDoDono)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, 'sumido@exemplo.com', $2)`, usuario, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(ctx,
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), usuario, familia, []byte("hash-da-sessao-do-sumido"), clk.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	gatilho := &apagaUsuarioNaLeitura{outro: pool, usuario: usuario}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = gatilho
	vigiado, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vigiado.Close)

	chaveDeIP := []byte("chave-de-teste-com-32-bytes-ok!!")
	segundoFator := auth.NewTOTP(vigiado, nil, "StillOn")
	return &cenarioDoUsuarioSumido{
		h: &AuthHandlers{
			users: auth.NewPGStore(vigiado), sessions: auth.NewManager(nil, clk, chaveDeIP), totp: segundoFator, clk: clk,
			jwtSecret: []byte("segredo-de-teste-com-32-bytes!!!"),
			provas: &provasDaFamilia{
				contador: auth.NewFamilyProofs(pool, clk),
				contas:   account.NewService(pool, auth.NewTOTP(pool, nil, "StillOn"), clk, chaveDeIP),
			},
		},
		pool: pool, gatilho: gatilho, clk: clk, usuario: usuario, familia: familia,
	}
}

func (c *cenarioDoUsuarioSumido) pedir(t *testing.T, rota string, handler http.HandlerFunc, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	acesso, err := auth.NewAccessToken(c.h.jwtSecret, c.usuario, c.familia, c.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, rota, strings.NewReader(corpo))
	r.Header.Set("Authorization", "Bearer "+acesso)
	w := httptest.NewRecorder()
	middleware.Authn(c.h.jwtSecret, c.clk)(handler).ServeHTTP(w, r)
	return w
}

// O usuário do token que some no meio da requisição é 401 unauthorized em toda
// rota autenticada do 2FA, e nunca 500: o painel volta ao login. O corpo é o
// envelope genérico, sem o erro do banco, sem o nome da operação e sem nada
// do que a rota entregaria no sucesso. Sabotagem: usuarioOuInterno mandar
// tudo para h.internal.
func TestRotas2FAUsuarioSumidoDevolve401(t *testing.T) {
	rotas := []struct {
		nome, rota, corpo string
		handler           func(*AuthHandlers) http.HandlerFunc
	}{
		{"enroll", "/api/v1/auth/totp/enroll", fmt.Sprintf(`{"password":%q}`, senhaDoDono),
			func(h *AuthHandlers) http.HandlerFunc { return h.TOTPEnroll }},
		{"confirm", "/api/v1/auth/totp/confirm", `{"code":"123456"}`,
			func(h *AuthHandlers) http.HandlerFunc { return h.TOTPConfirm }},
		{"disable", "/api/v1/auth/totp/disable", fmt.Sprintf(`{"password":%q,"code":"123456"}`, senhaDoDono),
			func(h *AuthHandlers) http.HandlerFunc { return h.TOTPDisable }},
		{"rotate", "/api/v1/auth/totp/rotate", fmt.Sprintf(`{"password":%q,"code":"123456"}`, senhaDoDono),
			func(h *AuthHandlers) http.HandlerFunc { return h.TOTPRotate }},
		{"rotate/confirm", "/api/v1/auth/totp/rotate/confirm", `{"code":"123456","rotation":"troca-qualquer"}`,
			func(h *AuthHandlers) http.HandlerFunc { return h.TOTPRotateConfirm }},
	}
	for _, rt := range rotas {
		t.Run(rt.nome, func(t *testing.T) {
			c := novoCenarioDoUsuarioSumido(t)

			w := c.pedir(t, rt.rota, rt.handler(c.h), rt.corpo)

			if err, _ := c.gatilho.erro.Load().(error); err != nil {
				t.Fatalf("apagar o usuário no meio: %v", err)
			}
			if !c.gatilho.feito.Load() {
				t.Fatal("a rota não leu a conta pelo pool vigiado; o cenário não aconteceu")
			}
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, queria 401: %s", w.Code, w.Body.String())
			}
			if codigo := codigoDoEnvelope(t, w.Body.Bytes()); codigo != "unauthorized" {
				t.Errorf("code = %q, queria unauthorized", codigo)
			}
			if cookies := w.Header().Values("Set-Cookie"); len(cookies) != 0 {
				t.Errorf("a recusa mexeu nos cookies: %v", cookies)
			}
			for _, proibido := range []string{
				"registro não encontrado", "buscar usuário", "TOTP", "SQL", "pgx", "users",
				"otpauth", "recovery_codes", "rotation", "sumido@exemplo.com", c.usuario.String(),
			} {
				if strings.Contains(w.Body.String(), proibido) {
					t.Errorf("o corpo trouxe %q: %s", proibido, w.Body.String())
				}
			}
		})
	}
}

// contasComEmail é o duble do segundo fator com o ByEmail do login.
type contasComEmail struct{ *contasDoDuble }

func (c contasComEmail) ByEmail(context.Context, string) (auth.User, error) {
	return c.usuario, nil
}

// Banco fora ao consultar se a conta tem 2FA, depois da senha certa: 500
// genérico, e a tentativa reservada no bloqueio da conta volta. Contada, um
// defeito do servidor trancaria o dono. Nenhum desafio nem cookie sai.
// Sabotagens: tirar o devolverTentativa do ramo; responder
// invalid_credentials no lugar do 500.
func TestLoginFalhaAoConsultar2FADevolveTentativa(t *testing.T) {
	c := novoCenarioDoSegundoFator(t) // o segundo fator está na porta fechada
	c.h.users = contasComEmail{c.contas}
	corpo := fmt.Sprintf(`{"email":"dono@exemplo.com","password":%q}`, senhaDoDono)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(corpo))
	w := httptest.NewRecorder()

	c.h.Login(w, r)

	conferirErroInternoGenerico(t, w)
	for _, proibido := range []string{"challenge", "totp_required", "access_token", "consultar segundo fator"} {
		if strings.Contains(w.Body.String(), proibido) {
			t.Errorf("o corpo trouxe %q: %s", proibido, w.Body.String())
		}
	}
	if c.contas.reservas != 1 || c.contas.devolucoes != 1 || c.contas.zeradas != 0 {
		t.Errorf("reservas = %d, devoluções = %d, zeradas = %d; queria 1, 1 e 0",
			c.contas.reservas, c.contas.devolucoes, c.contas.zeradas)
	}
}

// contasQueNaoReservam falha ao reservar a tentativa do bloqueio da conta.
type contasQueNaoReservam struct{ *contasDoDuble }

func (c contasQueNaoReservam) ReserveAttempt(context.Context, uuid.UUID, []byte, time.Time) (bool, error) {
	c.reservas++
	return false, errors.New("banco fora ao reservar")
}

// Banco fora ao reservar a tentativa do /totp/verify: 500 genérico, e nada
// mais acontece. Não há reserva a devolver, o contador não é zerado e o
// código nem é julgado (o UserStore nil do duble derrubaria o teste num
// ConsumeChallenge). Sabotagens: tratar o erro como "não reservou" (401);
// seguir como se tivesse reservado (o TOTP falha e devolve uma reserva que não
// existe).
func TestTOTPVerifyFalhaAoReservarTentativaDevolve500(t *testing.T) {
	c := novoCenarioDoSegundoFator(t)
	c.h.users = contasQueNaoReservam{c.contas}

	w := c.verificarCodigo(t)

	conferirErroInternoGenerico(t, w)
	if strings.Contains(w.Body.String(), "banco fora") || strings.Contains(w.Body.String(), "reservar") {
		t.Errorf("o corpo trouxe o erro interno: %s", w.Body.String())
	}
	if c.contas.reservas != 1 || c.contas.devolucoes != 0 || c.contas.zeradas != 0 {
		t.Errorf("reservas = %d, devoluções = %d, zeradas = %d; queria 1, 0 e 0",
			c.contas.reservas, c.contas.devolucoes, c.contas.zeradas)
	}
}
