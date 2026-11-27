package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/auth"
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
// senha recebida.
type cadastroQueDevolve struct {
	err   error
	senha *string
}

func (c cadastroQueDevolve) Cadastrar(context.Context, string, string) error { return nil }
func (c cadastroQueDevolve) ConfirmarEmail(_ context.Context, _, senha string) error {
	*c.senha = senha
	return c.err
}
func (c cadastroQueDevolve) ReenviarConfirmacao(context.Context, uuid.UUID) error { return nil }

// Cada erro do serviço na confirmação vira a resposta do contrato, e a senha do
// corpo chega ao serviço como veio.
func TestConfirmarEmailRespostasDoContrato(t *testing.T) {
	casos := map[string]struct {
		err        error
		status     int
		codigo     string
		retryAfter string
	}{
		"confirmado":        {err: nil, status: http.StatusNoContent},
		"token inválido":    {err: account.ErrTokenInvalido, status: http.StatusBadRequest, codigo: `"token_invalid"`},
		"senha errada":      {err: account.ErrInvalidCredentials, status: http.StatusUnauthorized, codigo: `"invalid_credentials"`},
		"conta travada":     {err: auth.ErrLocked, status: http.StatusTooManyRequests, codigo: `"rate_limited"`, retryAfter: "900"},
		"Argon2id sem vaga": {err: auth.ErrArgon2Busy, status: http.StatusTooManyRequests, codigo: `"rate_limited"`, retryAfter: "2"},
		"falha de infra":    {err: errors.New("banco fora"), status: http.StatusInternalServerError, codigo: `"internal_error"`},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			var recebida string
			h := &CadastroHandlers{cadastro: cadastroQueDevolve{err: c.err, senha: &recebida}}
			rec := httptest.NewRecorder()
			h.ConfirmarEmail(rec, httptest.NewRequest(http.MethodPost, "/",
				strings.NewReader(`{"token":"um-token","password":"a senha digitada"}`)))
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.codigo) {
				t.Errorf("%d %s, queria %d %s", rec.Code, rec.Body, c.status, c.codigo)
			}
			if got := rec.Header().Get("Retry-After"); got != c.retryAfter {
				t.Errorf("Retry-After = %q, queria %q", got, c.retryAfter)
			}
			if recebida != "a senha digitada" {
				t.Errorf("o serviço recebeu a senha %q", recebida)
			}
		})
	}
}
