package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
)

// O token vazio ou comprido demais é token_invalid sem consultar o serviço:
// os handlers não têm serviço nenhum, e chegar a ele seria panic.
func TestTokenImplausivelNaoChegaAoServico(t *testing.T) {
	h := &CadastroHandlers{}
	comprido := strings.Repeat("A", tamanhoMaximoDoTokenDeConta+1)
	casos := map[string]struct {
		handler http.HandlerFunc
		corpo   string
	}{
		"confirmar, comprido": {h.ConfirmarEmail, `{"token":"` + comprido + `"}`},
		"confirmar, vazio":    {h.ConfirmarEmail, `{"token":""}`},
		"redefinir, comprido": {h.RedefinirSenha, `{"token":"` + comprido + `","new_password":"uma-senha-bem-comprida"}`},
		"redefinir, vazio":    {h.RedefinirSenha, `{"new_password":"uma-senha-bem-comprida"}`},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			rec := httptest.NewRecorder()
			defer func() {
				if recover() != nil {
					t.Fatal("o token implausível chegou ao serviço")
				}
			}()
			c.handler(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(c.corpo)))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"token_invalid"`) {
				t.Errorf("%d %s, queria 400 token_invalid", rec.Code, rec.Body)
			}
		})
	}
}

// cadastroQueDevolve é o Cadastrador que devolve err na confirmação e guarda a
// senha e o ip_hash recebidos.
type cadastroQueDevolve struct {
	err    error
	senha  *string
	ipHash *[]byte
}

func (c cadastroQueDevolve) Cadastrar(context.Context, string, string, string) error { return nil }
func (c cadastroQueDevolve) ConfirmarEmail(_ context.Context, _, senha string, ipHash []byte) error {
	*c.senha, *c.ipHash = senha, ipHash
	return c.err
}
func (c cadastroQueDevolve) ReenviarConfirmacao(context.Context, uuid.UUID) error { return nil }

var chaveDeIPDoTeste = []byte("chave-de-ip-sintetica-so-para-teste")

// Cada erro do serviço na confirmação vira a resposta do contrato; a senha do
// corpo chega ao serviço como veio, com o HMAC do IP do cliente (o mesmo do
// login), que a auditoria da trava grava. A trava manda no Retry-After o que
// falta dela, arredondado para cima.
func TestConfirmarEmailRespostasDoContrato(t *testing.T) {
	agora := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	casos := map[string]struct {
		err        error
		status     int
		codigo     string
		retryAfter string
	}{
		"confirmado":     {err: nil, status: http.StatusNoContent},
		"token inválido": {err: account.ErrTokenInvalido, status: http.StatusBadRequest, codigo: `"token_invalid"`},
		"senha errada":   {err: account.ErrInvalidCredentials, status: http.StatusUnauthorized, codigo: `"invalid_credentials"`},
		"conta travada": {err: auth.LockedError{Until: agora.Add(90*time.Second + 500*time.Millisecond)},
			status: http.StatusTooManyRequests, codigo: `"rate_limited"`, retryAfter: "91"},
		"Argon2id sem vaga": {err: auth.ErrArgon2Busy, status: http.StatusTooManyRequests, codigo: `"rate_limited"`, retryAfter: "2"},
		"falha de infra":    {err: errors.New("banco fora"), status: http.StatusInternalServerError, codigo: `"internal_error"`},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			var recebida string
			var ipHash []byte
			h := &CadastroHandlers{
				cadastro: cadastroQueDevolve{err: c.err, senha: &recebida, ipHash: &ipHash},
				ipKey:    chaveDeIPDoTeste, clk: clock.NewFake(agora),
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/",
				strings.NewReader(`{"token":"um-token","password":"a senha digitada"}`))
			req.RemoteAddr = "198.51.100.7:4000"
			h.ConfirmarEmail(rec, req)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.codigo) {
				t.Errorf("%d %s, queria %d %s", rec.Code, rec.Body, c.status, c.codigo)
			}
			if got := rec.Header().Get("Retry-After"); got != c.retryAfter {
				t.Errorf("Retry-After = %q, queria %q", got, c.retryAfter)
			}
			if recebida != "a senha digitada" {
				t.Errorf("o serviço recebeu a senha %q", recebida)
			}
			if !bytes.Equal(ipHash, crypto.HashIP(chaveDeIPDoTeste, "198.51.100.7")) {
				t.Errorf("o serviço recebeu o ip_hash %x, queria o HMAC do IP do cliente", ipHash)
			}
		})
	}
}

// O Retry-After da trava: o que falta, arredondado para cima, e nunca menos de
// 1 s (a trava que venceu entre a recusa e a resposta).
func TestSegundosAteOFimDaTrava(t *testing.T) {
	agora := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	casos := []struct {
		falta time.Duration
		quer  int
	}{
		{15 * time.Minute, 900},
		{899*time.Second + time.Millisecond, 900},
		{time.Second, 1},
		{time.Millisecond, 1},
		{0, 1},
		{-time.Minute, 1},
	}
	for _, c := range casos {
		if got := segundosAte(agora.Add(c.falta), agora); got != c.quer {
			t.Errorf("faltando %v: %d s, queria %d", c.falta, got, c.quer)
		}
	}
}
