package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
)

func prefixos(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func requisicaoDe(remoto string, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.RemoteAddr = remoto
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// TestClientIPIgnoresForwardedForFromAnUntrustedOrigin é a perna que impede a
// correção de ser pior que o defeito. X-Forwarded-For é um cabeçalho de
// REQUISIÇÃO: quem escolhe o valor é o cliente. Acreditar nele sem saber de
// quem veio deixa o atacante escolher um balde por requisição — o limitador
// vira zero e o ip_hash vira origem falsa atribuída a terceiros.
func TestClientIPIgnoresForwardedForFromAnUntrustedOrigin(t *testing.T) {
	r := requisicaoDe("203.0.113.9:4444", "1.2.3.4")

	if got := middleware.ClientIP(r, nil); got != "203.0.113.9" {
		t.Errorf("sem proxy confiável, ClientIP = %q, queria o RemoteAddr 203.0.113.9", got)
	}

	confiaveis := prefixos(t, "10.0.0.0/8")
	if got := middleware.ClientIP(r, confiaveis); got != "203.0.113.9" {
		t.Errorf("origem fora da lista confiável, ClientIP = %q, queria 203.0.113.9", got)
	}
}

// TestClientIPTakesTheLastHopFromATrustedProxy: o último salto, e não o
// primeiro. No Fly.io o proxy ANEXA o IP real ao fim da cadeia, então o
// último elemento é o único que o proxy escreveu e o cliente não controla.
// Os anteriores foram enviados pelo cliente e são tão confiáveis quanto ele.
func TestClientIPTakesTheLastHopFromATrustedProxy(t *testing.T) {
	r := requisicaoDe("10.1.2.3:5555", "9.9.9.9, 8.8.8.8, 198.51.100.7")
	confiaveis := prefixos(t, "10.0.0.0/8")

	if got := middleware.ClientIP(r, confiaveis); got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, queria o último salto 198.51.100.7", got)
	}
}

// Um XFF ilegível não pode virar chave de balde nem hash de origem.
func TestClientIPFallsBackWhenTheForwardedHeaderIsUnusable(t *testing.T) {
	confiaveis := prefixos(t, "10.0.0.0/8")

	for _, xff := range []string{"nao-e-um-ip", "", "  ,  "} {
		r := requisicaoDe("10.1.2.3:5555", xff)
		if xff == "" {
			r.Header.Del("X-Forwarded-For")
		}
		if got := middleware.ClientIP(r, confiaveis); got != "10.1.2.3" {
			t.Errorf("com XFF %q, ClientIP = %q, queria o fallback 10.1.2.3", xff, got)
		}
	}
}

// TestRateLimitSeparatesClientsBehindATrustedProxy é o dano que o item 21
// descreve: hoje 20 clientes distintos atrás do proxy dividem um balde só, e
// o primeiro deles derruba o login de todos os outros.
func TestRateLimitSeparatesClientsBehindATrustedProxy(t *testing.T) {
	ctx := t.Context()
	confiaveis := prefixos(t, "10.0.0.0/8")
	h := middleware.RateLimit(ctx, 60, 1, confiaveis)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	for i, xff := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		r := requisicaoDe("10.1.2.3:5555", xff)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("cliente %d (%s) recebeu %d: gastou o balde de outro cliente", i, xff, w.Code)
		}
	}
}
