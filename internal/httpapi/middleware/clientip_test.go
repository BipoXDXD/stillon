package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
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

	if got := middleware.ClientIP(r, middleware.Proxies{}); got != "203.0.113.9" {
		t.Errorf("sem proxy confiável, ClientIP = %q, queria o RemoteAddr 203.0.113.9", got)
	}

	confiaveis := prefixos(t, "10.0.0.0/8")
	if got := middleware.ClientIP(r, middleware.Proxies{Trusted: confiaveis}); got != "203.0.113.9" {
		t.Errorf("origem fora da lista confiável, ClientIP = %q, queria 203.0.113.9", got)
	}
}

// TestClientIPTakesTheLastHopFromATrustedProxy: o último salto, e não o
// primeiro. Um proxy que ANEXA o IP observado ao fim da cadeia escreveu só o
// último elemento; os anteriores foram enviados pelo cliente e são tão
// confiáveis quanto ele. (O Fly anexa o IP do próprio app, e por isso lá se
// usa o cabeçalho dedicado: ver os testes de Fly-Client-IP.)
func TestClientIPTakesTheLastHopFromATrustedProxy(t *testing.T) {
	r := requisicaoDe("10.1.2.3:5555", "9.9.9.9, 8.8.8.8, 198.51.100.7")
	confiaveis := prefixos(t, "10.0.0.0/8")

	if got := middleware.ClientIP(r, middleware.Proxies{Trusted: confiaveis}); got != "198.51.100.7" {
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
		if got := middleware.ClientIP(r, middleware.Proxies{Trusted: confiaveis}); got != "10.1.2.3" {
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
	h := middleware.RateLimit(ctx, 60, 1, middleware.Proxies{Trusted: confiaveis})(http.HandlerFunc(
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

// Formato do Fly (documentação de request headers): o X-Forwarded-For
// termina no IP do APP, e o cliente vem em Fly-Client-IP.
const ipDoApp = "66.241.124.63"

func requisicaoDoFly(remoto, cliente string) *http.Request {
	r := requisicaoDe(remoto, cliente+", "+ipDoApp)
	r.Header.Set("Fly-Client-IP", cliente)
	return r
}

func fly(t *testing.T) middleware.Proxies {
	return middleware.Proxies{Trusted: prefixos(t, "172.16.0.0/12"), Header: "Fly-Client-IP"}
}

// TestClientIPReadsFlyClientIPFromATrustedProxy é o caminho feliz da D22 do
// Plano 7.
func TestClientIPReadsFlyClientIPFromATrustedProxy(t *testing.T) {
	r := requisicaoDoFly("172.19.0.5:5555", "198.51.100.7")
	if got := middleware.ClientIP(r, fly(t)); got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, queria o Fly-Client-IP 198.51.100.7", got)
	}
}

// TestClientIPWithoutTheHeaderSettingCollapsesBehindFly documenta o defeito
// que o CLIENT_IP_HEADER corrige: no formato do Fly, o último salto do XFF é
// o IP do app, igual para todos os clientes. O padrão continua o mesmo (e
// fechado); é a configuração que muda.
func TestClientIPWithoutTheHeaderSettingCollapsesBehindFly(t *testing.T) {
	semHeader := middleware.Proxies{Trusted: prefixos(t, "172.16.0.0/12")}
	for _, cliente := range []string{"198.51.100.1", "203.0.113.9"} {
		r := requisicaoDoFly("172.19.0.5:5555", cliente)
		if got := middleware.ClientIP(r, semHeader); got != ipDoApp {
			t.Errorf("sem CLIENT_IP_HEADER, ClientIP = %q; o formato do Fly daria o IP do app %s", got, ipDoApp)
		}
	}
}

// TestClientIPIgnoresAForgedFlyClientIP: o cabeçalho vale só vindo da lista.
// Direto da internet (ou de outra máquina fora da lista), é o cliente
// escolhendo o próprio balde.
func TestClientIPIgnoresAForgedFlyClientIP(t *testing.T) {
	r := requisicaoDoFly("203.0.113.50:4444", "1.2.3.4")
	if got := middleware.ClientIP(r, fly(t)); got != "203.0.113.50" {
		t.Errorf("origem fora da lista, ClientIP = %q, queria o RemoteAddr 203.0.113.50", got)
	}
	vazia := middleware.Proxies{Header: "Fly-Client-IP"}
	if got := middleware.ClientIP(r, vazia); got != "203.0.113.50" {
		t.Errorf("TRUSTED_PROXIES vazio, ClientIP = %q, queria o RemoteAddr 203.0.113.50", got)
	}
}

// Com o cabeçalho configurado, o X-Forwarded-For não conta mais nada, nem
// quando é o único que veio.
func TestClientIPWithTheHeaderSettingIgnoresForwardedFor(t *testing.T) {
	r := requisicaoDe("172.19.0.5:5555", "198.51.100.7")
	if got := middleware.ClientIP(r, fly(t)); got != "172.19.0.5" {
		t.Errorf("sem Fly-Client-IP, ClientIP = %q, queria o RemoteAddr 172.19.0.5", got)
	}
}

// Um Fly-Client-IP ilegível não pode virar chave de balde nem hash de origem.
func TestClientIPFallsBackWhenFlyClientIPIsUnusable(t *testing.T) {
	for _, valor := range []string{"nao-e-um-ip", "1.2.3.4, 5.6.7.8", "fe80::1%eth0", " "} {
		r := requisicaoDe("172.19.0.5:5555", "")
		r.Header.Set("Fly-Client-IP", valor)
		if got := middleware.ClientIP(r, fly(t)); got != "172.19.0.5" {
			t.Errorf("com Fly-Client-IP %q, ClientIP = %q, queria o fallback 172.19.0.5", valor, got)
		}
	}
}

// IPv4 mapeado em IPv6 vira o IPv4: o mesmo cliente não ganha dois baldes.
func TestClientIPUnmapsFlyClientIP(t *testing.T) {
	r := requisicaoDoFly("172.19.0.5:5555", "::ffff:198.51.100.7")
	if got := middleware.ClientIP(r, fly(t)); got != "198.51.100.7" {
		t.Errorf("ClientIP = %q, queria 198.51.100.7", got)
	}
}

// TestRateLimitSeparatesClientsBehindFly é o dano real da pendência mais
// antiga do projeto: no formato do Fly, sem o cabeçalho dedicado, todos os
// clientes caem no balde do IP do app.
func TestRateLimitSeparatesClientsBehindFly(t *testing.T) {
	h := middleware.RateLimit(t.Context(), 60, 1, fly(t))(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	for i, cliente := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, requisicaoDoFly("172.19.0.5:5555", cliente))
		if w.Code != http.StatusOK {
			t.Errorf("cliente %d (%s) recebeu %d: gastou o balde de outro cliente", i, cliente, w.Code)
		}
	}
}

// Plano 8: todas as saídas de ClientIP têm a mesma forma. Antes, só o
// Fly-Client-IP passava por Unmap; o endereço da conexão e o último salto do
// X-Forwarded-For saíam crus, e o mesmo cliente IPv4 numa pilha dupla ganhava
// dois ip_hash na auditoria e duas origens em GET /sessions.
func TestClientIPIsTheSameForAMappedAndAPlainClient(t *testing.T) {
	confiaveis := middleware.Proxies{Trusted: prefixos(t, "10.0.0.0/8")}
	casos := []struct {
		nome          string
		mapeado, puro *http.Request
		px            middleware.Proxies
	}{
		{"endereço da conexão, sem proxy",
			requisicaoDe("[::ffff:203.0.113.9]:4444", ""), requisicaoDe("203.0.113.9:4444", ""),
			middleware.Proxies{}},
		{"endereço da conexão, fora da lista",
			requisicaoDe("[::ffff:203.0.113.9]:4444", "1.2.3.4"), requisicaoDe("203.0.113.9:4444", "1.2.3.4"),
			confiaveis},
		{"último salto do X-Forwarded-For",
			requisicaoDe("10.1.2.3:5555", "::ffff:198.51.100.7"), requisicaoDe("10.1.2.3:5555", "198.51.100.7"),
			confiaveis},
		{"proxy confiável mapeado, sem X-Forwarded-For",
			requisicaoDe("[::ffff:10.1.2.3]:5555", ""), requisicaoDe("10.1.2.3:5555", ""),
			confiaveis},
	}
	for _, c := range casos {
		m, p := middleware.ClientIP(c.mapeado, c.px), middleware.ClientIP(c.puro, c.px)
		if m != p || strings.Contains(m, ":") {
			t.Errorf("%s: mapeado = %q, puro = %q; queria os dois iguais e em IPv4", c.nome, m, p)
		}
	}
}

// Uma zona no último salto é texto do cliente, e não um endereço de onde a
// requisição possa ter vindo: cai no endereço da conexão, como o ilegível.
func TestClientIPRejectsAZonedForwardedHop(t *testing.T) {
	r := requisicaoDe("10.1.2.3:5555", "fe80::1%eth0")
	if got := middleware.ClientIP(r, middleware.Proxies{Trusted: prefixos(t, "10.0.0.0/8")}); got != "10.1.2.3" {
		t.Errorf("ClientIP = %q, queria o fallback 10.1.2.3", got)
	}
}

// O que não é IP no RemoteAddr (listener de unix socket) continua como veio.
func TestClientIPKeepsANonIPConnectionAddress(t *testing.T) {
	r := requisicaoDe("@", "")
	if got := middleware.ClientIP(r, middleware.Proxies{}); got != "@" {
		t.Errorf("ClientIP = %q, queria @", got)
	}
}

// Um IPv6 escrito de outro jeito (maiúsculas, zeros explícitos) é o mesmo
// cliente: sai na forma canônica, e ganha um ip_hash só.
func TestClientIPWritesIPv6InTheCanonicalForm(t *testing.T) {
	confiaveis := middleware.Proxies{Trusted: prefixos(t, "10.0.0.0/8")}
	for _, grafia := range []string{"2001:DB8:0:0::1", "2001:0db8::0001", "2001:db8::1"} {
		if got := middleware.ClientIP(requisicaoDe("10.1.2.3:5555", grafia), confiaveis); got != "2001:db8::1" {
			t.Errorf("último salto %q: ClientIP = %q, queria 2001:db8::1", grafia, got)
		}
	}
	if got := middleware.ClientIP(requisicaoDe("[2001:DB8::1]:4444", ""), middleware.Proxies{}); got != "2001:db8::1" {
		t.Errorf("endereço da conexão: ClientIP = %q, queria 2001:db8::1", got)
	}
}

// B2 (Plano 10): um proxy que acrescenta uma LINHA nova de X-Forwarded-For
// (o HAProxy com option forwardfor), em vez de concatenar, deixa a linha do
// cliente em primeiro lugar. As linhas são lidas juntas, na ordem, e o último
// salto é o da última linha, que é a do proxy.
func TestClientIPJoinsEveryForwardedForLine(t *testing.T) {
	confiaveis := middleware.Proxies{Trusted: prefixos(t, "10.0.0.0/8")}
	cases := []struct {
		nome   string
		linhas []string
		quer   string
	}{
		{"linha forjada antes da do proxy", []string{"6.6.6.6", "198.51.100.7"}, "198.51.100.7"},
		{"cadeia na primeira e o proxy na segunda", []string{"6.6.6.6, 7.7.7.7", "198.51.100.7"}, "198.51.100.7"},
		{"cadeia na última linha", []string{"6.6.6.6", "7.7.7.7, 198.51.100.7"}, "198.51.100.7"},
		{"última linha ilegível", []string{"198.51.100.7", "nao-e-ip"}, "10.1.2.3"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			r := requisicaoDe("10.1.2.3:5555", "")
			for _, l := range c.linhas {
				r.Header.Add("X-Forwarded-For", l)
			}
			if got := middleware.ClientIP(r, confiaveis); got != c.quer {
				t.Errorf("linhas %q: ClientIP = %q, queria %q", c.linhas, got, c.quer)
			}
		})
	}
}
