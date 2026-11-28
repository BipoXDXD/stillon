package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/httpapi"
)

// cadastroQueMedePrazo é o cadastro sem banco que guarda quanto prazo o
// contexto do ConfirmarEmail ainda tinha.
type cadastroQueMedePrazo struct {
	contaPublicaFalsa
	restante *time.Duration
}

func (c cadastroQueMedePrazo) ConfirmarEmail(ctx context.Context, _, _ string, _ []byte) error {
	if d, ok := ctx.Deadline(); ok {
		*c.restante = time.Until(d)
	}
	return account.ErrTokenInvalido
}

// O prazo longo é só da exportação: com ele em 1 h, o resto da API continua
// com o RequestTimeout da configuração (50 ms aqui).
func TestPrazoDaExportacaoNaoValeParaAsOutrasRotas(t *testing.T) {
	const global = 50 * time.Millisecond
	restante := time.Duration(-1)
	rotas := rotasSemBancoCom(t, configSemBanco(global), nil, func(d *httpapi.Deps) {
		d.PrazoExportacao = time.Hour
		d.Cadastro = cadastroQueMedePrazo{restante: &restante}
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/email/confirm",
		strings.NewReader(`{"token":"tok","password":"senha"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "stillon")
	w := httptest.NewRecorder()
	rotas.ServeHTTP(w, req)
	if restante <= 0 || restante > global {
		t.Fatalf("prazo restante no confirm = %v (status %d %s), queria entre 0 e %v", restante, w.Code, w.Body, global)
	}
}

// O limitador por IP é uma instância só: pedidos à exportação gastam o mesmo
// balde (rajada de 30, relógio parado) que o resto da API, nos dois sentidos.
func TestExportacaoDivideOLimitePorIPComAAPI(t *testing.T) {
	for _, c := range []struct{ nome, gasta, depois string }{
		{"exportação esgota a API", "/api/v1/me/export", "/api/v1/me"},
		{"API esgota a exportação", "/api/v1/me", "/api/v1/me/export"},
	} {
		t.Run(c.nome, func(t *testing.T) {
			rotas := rotasSemBanco(t, configSemBanco(15*time.Second), nil)
			for i := range 30 {
				w := httptest.NewRecorder()
				rotas.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.gasta, nil))
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("pedido %d a %s: %d, queria 401 (sem token)", i+1, c.gasta, w.Code)
				}
			}
			w := httptest.NewRecorder()
			rotas.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.depois, nil))
			if w.Code != http.StatusTooManyRequests {
				t.Errorf("%s depois de 30 pedidos a %s: %d, queria 429 (mesmo balde por IP)", c.depois, c.gasta, w.Code)
			}
		})
	}
}
