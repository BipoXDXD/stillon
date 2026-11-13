package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/storage"
)

// novaReplica sobe outra instância da API sobre o MESMO banco de a: rotas,
// middlewares e orçamento próprios, como um segundo processo. Só o banco é
// comum, e é ele que tem de somar as tentativas.
func novaReplica(t *testing.T, a *api) *httptest.Server {
	t.Helper()
	deps := a.deps
	deps.LimitesIP = storage.NewOrcamentosPorIP(a.pool, a.clk)
	srv := httptest.NewServer(httpapi.Routes(a.ctx, a.cfg, deps))
	t.Cleanup(srv.Close)
	return srv
}

type rotaPorIP struct {
	caminho    string
	corpo      map[string]string
	dentro     int
	rajada     int
	retryAfter string
}

var (
	loginPorIP = rotaPorIP{
		caminho: "/api/v1/auth/login",
		corpo:   map[string]string{"email": "ninguem@exemplo.com", "password": "senha-errada-de-proposito"},
		dentro:  http.StatusUnauthorized, rajada: 5, retryAfter: "6",
	}
	verificacaoTOTPPorIP = rotaPorIP{
		caminho: "/api/v1/auth/totp/verify",
		corpo:   map[string]string{"challenge": "desafio-invalido", "code": "000000"},
		dentro:  http.StatusUnauthorized, rajada: 5, retryAfter: "3",
	}
)

func pedirNaReplica(t *testing.T, srv *httptest.Server, r rotaPorIP) *http.Response {
	t.Helper()
	resp := post(t, http.DefaultClient, srv.URL+r.caminho, r.corpo)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// A rajada de uma origem é uma só, seja qual for a réplica que atende: os
// pedidos alternam entre duas instâncias, e o primeiro além da rajada recebe
// 429 nas duas. Com o orçamento na memória de cada processo, cada réplica daria
// a rajada inteira, e o dobro de tentativas passaria.
func TestTwoReplicasShareTheIPBudget(t *testing.T) {
	for _, r := range []rotaPorIP{loginPorIP, verificacaoTOTPPorIP} {
		t.Run(r.caminho, func(t *testing.T) {
			a := newAPI(t)
			replicas := []*httptest.Server{novaReplica(t, a), novaReplica(t, a)}

			for i := range r.rajada {
				if resp := pedirNaReplica(t, replicas[i%2], r); resp.StatusCode != r.dentro {
					t.Fatalf("pedido %d da rajada na réplica %d: %d, queria %d", i+1, i%2, resp.StatusCode, r.dentro)
				}
			}
			for i, srv := range replicas {
				resp := pedirNaReplica(t, srv, r)
				if resp.StatusCode != http.StatusTooManyRequests {
					t.Fatalf("réplica %d além da rajada somada: %d, queria 429", i, resp.StatusCode)
				}
				if got := resp.Header.Get("Retry-After"); got != r.retryAfter {
					t.Errorf("réplica %d: Retry-After = %q, queria %q", i, got, r.retryAfter)
				}
			}
		})
	}
}

// Login e /totp/verify têm baldes próprios, como antes, quando eram dois
// limitadores: esgotar um não cobra do outro.
func TestLoginAndTOTPVerifyKeepSeparateIPBudgets(t *testing.T) {
	a := newAPI(t)
	srv := novaReplica(t, a)
	for range loginPorIP.rajada {
		pedirNaReplica(t, srv, loginPorIP)
	}
	if resp := pedirNaReplica(t, srv, loginPorIP); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("login além da rajada: %d, queria 429", resp.StatusCode)
	}

	if resp := pedirNaReplica(t, srv, verificacaoTOTPPorIP); resp.StatusCode != verificacaoTOTPPorIP.dentro {
		t.Errorf("/totp/verify depois de esgotar o login: %d, queria %d", resp.StatusCode, verificacaoTOTPPorIP.dentro)
	}
}
