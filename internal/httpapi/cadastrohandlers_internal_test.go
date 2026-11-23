package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
