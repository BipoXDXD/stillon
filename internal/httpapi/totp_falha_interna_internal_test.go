package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const senhaDoDono = "senha-do-dono-bem-comprida"

// contasDoDuble conta o destino da tentativa do segundo fator. O que ele não
// implementa cai no UserStore nil e derruba o teste: depois de uma falha
// interna, o desafio não pode ser consumido.
type contasDoDuble struct {
	auth.UserStore

	usuario                       auth.User
	reservas, devolucoes, zeradas int
}

func (c *contasDoDuble) ByID(context.Context, uuid.UUID) (auth.User, error) {
	return c.usuario, nil
}

func (c *contasDoDuble) ReserveAttempt(context.Context, uuid.UUID, []byte, time.Time) (bool, error) {
	c.reservas++
	return true, nil
}

func (c *contasDoDuble) RefundAttempt(context.Context, uuid.UUID) error {
	c.devolucoes++
	return nil
}

func (c *contasDoDuble) ResetFailures(context.Context, uuid.UUID) error {
	c.zeradas++
	return nil
}

// poolDePortaFechada faz toda operação do segundo fator falhar por dentro,
// sem Docker: o auth.TOTP lê o banco por um *pgxpool.Pool concreto.
func poolDePortaFechada(t *testing.T) *pgxpool.Pool {
	t.Helper()
	escuta, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	porta := escuta.Addr().(*net.TCPAddr).Port
	if err := escuta.Close(); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), fmt.Sprintf(
		"postgres://stillon:x@127.0.0.1:%d/stillon?connect_timeout=1&pool_max_conns=2", porta))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type cenarioDoSegundoFator struct {
	h       *AuthHandlers
	contas  *contasDoDuble
	clk     *clock.Fake
	usuario uuid.UUID
	familia uuid.UUID
}

func novoCenarioDoSegundoFator(t *testing.T) cenarioDoSegundoFator {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword(ctx, senhaDoDono)
	if err != nil {
		t.Fatal(err)
	}
	usuario := uuid.New()
	contas := &contasDoDuble{usuario: auth.User{ID: usuario, Email: "dono@exemplo.com", PasswordHash: hash}}
	clk := clock.NewFake(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	chaveDeIP := []byte("chave-de-teste-com-32-bytes-ok!!")
	pool := poolDePortaFechada(t)
	segundoFator := auth.NewTOTP(pool, nil, "StillOn")
	return cenarioDoSegundoFator{
		h: &AuthHandlers{
			users: contas, sessions: auth.NewManager(nil, clk, chaveDeIP), totp: segundoFator, clk: clk,
			jwtSecret: []byte("segredo-de-teste-com-32-bytes!!!"),
			provas: &provasDaFamilia{
				contador: auth.NewFamilyProofs(pool, clk),
				contas:   account.NewService(pool, segundoFator, clk, chaveDeIP),
			},
		},
		contas: contas, clk: clk, usuario: usuario, familia: uuid.New(),
	}
}

// comContadorNoBanco troca o contador de provas por um sobre um Postgres real,
// com a família do cenário viva: o contador vive no banco, e o resto do cenário
// segue com a porta fechada, que é o que faz o segundo fator falhar por dentro.
func (c cenarioDoSegundoFator) comContadorNoBanco(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, 'dono@exemplo.com', 'x')`, c.usuario); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), c.usuario, c.familia, []byte("hash-da-sessao-do-cenario"), c.clk.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c.h.provas.contador = auth.NewFamilyProofs(pool, c.clk)
}

func (c cenarioDoSegundoFator) verificarCodigo(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	desafio, err := auth.NewChallengeToken(c.h.jwtSecret, c.usuario, auth.PasswordStamp("x"), c.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	corpo := fmt.Sprintf(`{"challenge":%q,"code":"123456"}`, desafio)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/verify", strings.NewReader(corpo))
	w := httptest.NewRecorder()
	c.h.TOTPVerify(w, r)
	return w
}

func (c cenarioDoSegundoFator) desligarFator(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	acesso, err := auth.NewAccessToken(c.h.jwtSecret, c.usuario, c.familia, c.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	corpo := fmt.Sprintf(`{"password":%q,"code":"123456"}`, senhaDoDono)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/disable", strings.NewReader(corpo))
	r.Header.Set("Authorization", "Bearer "+acesso)
	w := httptest.NewRecorder()
	middleware.Authn(c.h.jwtSecret, c.clk)(http.HandlerFunc(c.h.TOTPDisable)).ServeHTTP(w, r)
	return w
}

func conferirErroInternoGenerico(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, queria 500: %s", w.Code, w.Body.String())
	}
	if codigo := codigoDoEnvelope(t, w.Body.Bytes()); codigo != "internal_error" {
		t.Errorf("code = %q, queria internal_error", codigo)
	}
	if cookies := w.Header().Values("Set-Cookie"); len(cookies) != 0 {
		t.Errorf("a falha interna mexeu nos cookies: %v", cookies)
	}
	for _, detalhe := range []string{"carregar TOTP", "abrir transação", "connection refused"} {
		if strings.Contains(w.Body.String(), detalhe) {
			t.Errorf("o corpo trouxe %q: %s", detalhe, w.Body.String())
		}
	}
}

// Responder "credenciais inválidas" esconderia a causa da falha interna e
// ainda contaria a tentativa.
func TestTOTPVerifyInternalFailureAnswersAGeneric500(t *testing.T) {
	c := novoCenarioDoSegundoFator(t)

	conferirErroInternoGenerico(t, c.verificarCodigo(t))
}

// O código nem chegou a ser julgado: a reserva volta, senão o dono tranca a
// própria conta ao atingir o limite de tentativas com um defeito que não é dele.
func TestTOTPVerifyInternalFailureRefundsTheReservedAttempt(t *testing.T) {
	c := novoCenarioDoSegundoFator(t)

	c.verificarCodigo(t)

	if c.contas.reservas != 1 || c.contas.devolucoes != 1 || c.contas.zeradas != 0 {
		t.Errorf("reservas = %d, devoluções = %d, zeradas = %d; queria 1, 1 e 0",
			c.contas.reservas, c.contas.devolucoes, c.contas.zeradas)
	}
}

func TestTOTPDisableInternalFailureAnswersAGeneric500(t *testing.T) {
	c := novoCenarioDoSegundoFator(t)
	c.comContadorNoBanco(t)

	conferirErroInternoGenerico(t, c.desligarFator(t))
}

// Falha interna não é prova errada: contados, erros do servidor até o limite de
// provas da família derrubariam a sessão do dono.
func TestTOTPDisableInternalFailuresDoNotCountAsWrongProofs(t *testing.T) {
	c := novoCenarioDoSegundoFator(t)
	c.comContadorNoBanco(t)

	const falhasSeguidas = auth.FamilyProofLimit
	for range falhasSeguidas {
		if w := c.desligarFator(t); w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, queria 500: %s", w.Code, w.Body.String())
		}
	}

	seguinte, err := c.h.provas.contador.Reserve(context.Background(), c.usuario, c.familia)
	if err != nil {
		t.Fatal(err)
	}
	defer seguinte.Release()
	if seguinte.Exhausted() {
		t.Error("as falhas internas esgotaram o contador de provas da família")
	}
}
