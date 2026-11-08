package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
)

var instanteBase = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func proximo(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func pedir(h http.Handler, ajustar func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:4000"
	if ajustar != nil {
		ajustar(r)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestClientIPCabecalhoDedicadoSemEspacoNasPontas(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("Fly-Client-IP", " 203.0.113.9\t")
	px := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Header: "Fly-Client-IP"}
	if got := ClientIP(r, px); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, queria 203.0.113.9", got)
	}
}

func TestConexaoDeForaDaListaSemOCabecalhoNaoAvisa(t *testing.T) {
	agora := instanteBase
	log := capturarAvisos(t, &agora)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:4000"
	px := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Header: "Fly-Client-IP"}
	if got := ClientIP(r, px); got != "203.0.113.7" {
		t.Errorf("ClientIP = %q, queria 203.0.113.7", got)
	}
	if log.Len() != 0 {
		t.Errorf("avisou: %s", log.String())
	}
}

func TestClientIPReconheceProxyEmIPv4Mapeado(t *testing.T) {
	cases := []struct {
		nome   string
		remoto string
		quer   string
	}{
		{"proxy da lista", "[::ffff:10.0.0.1]:1234", "203.0.113.9"},
		{"fora da lista", "[::ffff:11.0.0.1]:1234", "11.0.0.1"},
	}
	px := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Header: "Fly-Client-IP"}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remoto
			r.Header.Set("Fly-Client-IP", "203.0.113.9")
			if got := ClientIP(r, px); got != c.quer {
				t.Errorf("ClientIP = %q, queria %q", got, c.quer)
			}
		})
	}
}

// 60 por minuto é uma ficha por segundo, nem antes nem depois.
func TestRateLimitRecarregaNaTaxaPedida(t *testing.T) {
	clk := clock.NewFake(instanteBase)
	h := RateLimit(t.Context(), clk, 60, 1, Proxies{})(http.HandlerFunc(proximo))
	if c := pedir(h, nil).Code; c != http.StatusOK {
		t.Fatalf("primeira: %d", c)
	}
	clk.Advance(999 * time.Millisecond)
	if c := pedir(h, nil).Code; c != http.StatusTooManyRequests {
		t.Fatalf("antes de 1 s: %d, queria 429", c)
	}
	clk.Advance(time.Millisecond)
	if c := pedir(h, nil).Code; c != http.StatusOK {
		t.Fatalf("depois de 1 s: %d, queria 200", c)
	}
}

func TestRateLimitByUsaAChaveDaRota(t *testing.T) {
	clk := clock.NewFake(instanteBase)
	chave := func(r *http.Request) string { return r.Header.Get("X-Chave") }
	h := RateLimitBy(t.Context(), clk, 1, 1, chave)(http.HandlerFunc(proximo))
	comChave := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("X-Chave", v) }
	}
	cases := []struct {
		chave string
		quer  int
	}{
		{"a", http.StatusOK},
		{"a", http.StatusTooManyRequests},
		{"b", http.StatusOK},
	}
	for i, c := range cases {
		if got := pedir(h, comChave(c.chave)).Code; got != c.quer {
			t.Errorf("pedido %d (chave %s): %d, queria %d", i, c.chave, got, c.quer)
		}
	}
}

func TestRateLimitUserDaUmBaldePorUsuario(t *testing.T) {
	ana := uuid.MustParse("01890000-0000-7000-8000-000000000001")
	bia := uuid.MustParse("01890000-0000-7000-8000-000000000002")
	clk := clock.NewFake(instanteBase)
	h := RateLimitUser(t.Context(), clk, 1, 1)(http.HandlerFunc(proximo))
	como := func(id uuid.UUID) func(*http.Request) {
		return func(r *http.Request) { *r = *r.WithContext(context.WithValue(r.Context(), userKey{}, id)) }
	}
	cases := []struct {
		nome    string
		usuario uuid.UUID
		quer    int
	}{
		{"primeira da Ana", ana, http.StatusOK},
		{"segunda da Ana", ana, http.StatusTooManyRequests},
		{"primeira da Bia", bia, http.StatusOK},
	}
	for _, c := range cases {
		if got := pedir(h, como(c.usuario)).Code; got != c.quer {
			t.Errorf("%s: %d, queria %d", c.nome, got, c.quer)
		}
	}
}

func TestRateLimitSemTaxaRecusaESemPrazo(t *testing.T) {
	clk := clock.NewFake(instanteBase)
	h := RateLimit(t.Context(), clk, 0, 0, Proxies{})(http.HandlerFunc(proximo))
	rec := pedir(h, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, queria 429", rec.Code)
	}
	if got := rec.Header().Values("Retry-After"); len(got) != 0 {
		t.Errorf("Retry-After = %v, queria ausente", got)
	}
}

// 7 por minuto: a próxima ficha sai em 8,57 s, e o cliente é mandado esperar 9.
func TestRetryAfterArredondaParaCima(t *testing.T) {
	clk := clock.NewFake(instanteBase)
	h := RateLimit(t.Context(), clk, 7, 1, Proxies{})(http.HandlerFunc(proximo))
	pedir(h, nil)
	if got := pedir(h, nil).Header().Get("Retry-After"); got != "9" {
		t.Errorf("Retry-After = %q, queria 9", got)
	}
}

func limitadorDeTeste(t *testing.T, clk clock.Clock, teto int) *limiterHandler {
	t.Helper()
	porCabecalho := func(r *http.Request) (string, bool) { return r.Header.Get("X-Chave"), true }
	return rateLimit(t.Context(), clk, rate.Limit(1), 5, porCabecalho, teto)(http.HandlerFunc(proximo)).(*limiterHandler)
}

func comChave(v string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("X-Chave", v) }
}

func varrerAgora(l *limiterHandler) {
	l.st.mu.Lock()
	defer l.st.mu.Unlock()
	l.st.varrer()
}

func TestVarreduraMedeOUltimoUso(t *testing.T) {
	cases := []struct {
		nome   string
		passos []time.Duration // avanço antes de cada uso depois do primeiro
		final  time.Duration
		restam int
	}{
		{"usado há pouco fica", []time.Duration{9 * time.Minute}, 9 * time.Minute, 1},
		{"exatamente no limite fica", nil, 10 * time.Minute, 1},
		{"um instante depois do limite sai", nil, 10*time.Minute + time.Nanosecond, 0},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			clk := clock.NewFake(instanteBase)
			l := limitadorDeTeste(t, clk, 10)
			pedir(l, comChave("a"))
			for _, d := range c.passos {
				clk.Advance(d)
				pedir(l, comChave("a"))
			}
			clk.Advance(c.final)
			varrerAgora(l)
			if got := quantosBuckets(l); got != c.restam {
				t.Errorf("restam %d baldes, queria %d", got, c.restam)
			}
		})
	}
}

func TestTempoParaEncher(t *testing.T) {
	cases := []struct {
		nome   string
		limite rate.Limit
		burst  int
		quer   time.Duration
	}{
		{"uma por segundo", rate.Limit(1), 5, 5 * time.Second},
		{"uma a cada 6 min", rate.Every(6 * time.Minute), 5, 30 * time.Minute},
		{"taxa zero", 0, 5, 0},
		{"taxa negativa", -1, 5, 0},
		{"taxa infinita", rate.Inf, 5, 0},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			if got := tempoParaEncher(c.limite, c.burst); got != c.quer {
				t.Errorf("tempoParaEncher = %v, queria %v", got, c.quer)
			}
		})
	}
}

// Um balde parado só sai depois de ter tido tempo de encher sozinho, e nunca
// antes do mínimo.
func TestBaldeParadoSaiDepoisDoTempoDeOciosidade(t *testing.T) {
	cases := []struct {
		nome   string
		limite rate.Limit
		burst  int
		prazo  time.Duration
	}{
		{"enche depressa: vale o mínimo", rate.Limit(1), 20, 10 * time.Minute},
		{"enche devagar: vale o tempo de encher", rate.Every(6 * time.Minute), 5, 30 * time.Minute},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			clk := clock.NewFake(instanteBase)
			porCabecalho := func(r *http.Request) (string, bool) { return r.Header.Get("X-Chave"), true }
			l := rateLimit(t.Context(), clk, c.limite, c.burst, porCabecalho, 10)(http.HandlerFunc(proximo)).(*limiterHandler)
			pedir(l, comChave("a"))
			clk.Advance(c.prazo)
			varrerAgora(l)
			if got := quantosBuckets(l); got != 1 {
				t.Fatalf("no prazo exato restam %d baldes, queria 1", got)
			}
			clk.Advance(time.Nanosecond)
			varrerAgora(l)
			if got := quantosBuckets(l); got != 0 {
				t.Errorf("depois do prazo restam %d baldes, queria 0", got)
			}
		})
	}
}

// Com o mapa cheio, os parados saem todos antes de despejar quem está em uso.
func TestTetoVarreOsParadosAntesDeDespejar(t *testing.T) {
	clk := clock.NewFake(instanteBase)
	l := limitadorDeTeste(t, clk, 3)
	pedir(l, comChave("parado-1"))
	pedir(l, comChave("parado-2"))
	clk.Advance(11 * time.Minute)
	pedir(l, comChave("recente"))
	pedir(l, comChave("novo"))
	if got := quantosBuckets(l); got != 2 {
		t.Errorf("restam %d baldes, queria 2 (recente e novo)", got)
	}
}

func TestRateLimitIPv6SeparaBlocosDe64(t *testing.T) {
	cases := []struct {
		nome    string
		remotos [2]string
		segunda int
		baldes  int
	}{
		{"mesmo /64 divide o balde", [2]string{"[2001:db8:1:2::1]:1", "[2001:db8:1:2:8000::1]:1"}, http.StatusTooManyRequests, 1},
		{"outro /64 no mesmo /48 não divide", [2]string{"[2001:db8:1:2::1]:1", "[2001:db8:1:3::1]:1"}, http.StatusOK, 2},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			clk := clock.NewFake(instanteBase)
			h := RateLimit(t.Context(), clk, 1, 1, Proxies{})(http.HandlerFunc(proximo))
			de := func(remoto string) func(*http.Request) {
				return func(r *http.Request) { r.RemoteAddr = remoto }
			}
			if got := pedir(h, de(c.remotos[0])).Code; got != http.StatusOK {
				t.Fatalf("primeira: %d", got)
			}
			if got := pedir(h, de(c.remotos[1])).Code; got != c.segunda {
				t.Errorf("segunda: %d, queria %d", got, c.segunda)
			}
			if got := quantosBuckets(h); got != c.baldes {
				t.Errorf("baldes = %d, queria %d", got, c.baldes)
			}
		})
	}
}

func TestRecoverRegistraODiagnostico(t *testing.T) {
	agora := instanteBase
	log := capturarAvisos(t, &agora)
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("valor-do-panic") })
	rec := pedir(RequestID(Recover(boom)), nil)
	trace := rec.Header().Get(TraceHeader)
	if trace == "" {
		t.Fatal("sem trace na resposta")
	}
	for _, trecho := range []string{"panic=valor-do-panic", "trace_id=" + trace, "goroutine "} {
		if !strings.Contains(log.String(), trecho) {
			t.Errorf("o log não traz %q: %s", trecho, log.String())
		}
	}
}

func TestSecurityHeadersValoresExatos(t *testing.T) {
	rec := pedir(SecurityHeaders(http.HandlerFunc(proximo)), nil)
	cases := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
		"Strict-Transport-Security":    "max-age=31536000; includeSubDomains",
		"Content-Security-Policy":      "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"X-Frame-Options":              "DENY",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
		"Cache-Control":                "no-store",
	}
	for nome, quer := range cases {
		t.Run(nome, func(t *testing.T) {
			if got := rec.Header().Get(nome); got != quer {
				t.Errorf("%s = %q, queria %q", nome, got, quer)
			}
		})
	}
}

func TestCORSDaOrigemConfigurada(t *testing.T) {
	const origem = "https://app.exemplo.com"
	chamado := false
	h := CORS(origem)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamado = true
		proximo(w, r)
	}))
	rec := pedir(h, func(r *http.Request) { r.Header.Set("Origin", origem) })
	if !chamado {
		t.Error("o CORS não chamou o próximo handler")
	}
	cases := map[string]string{
		"Access-Control-Allow-Origin":      origem,
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Allow-Headers":     "Content-Type, Authorization, X-Requested-With",
		"Access-Control-Allow-Methods":     "GET, POST, PATCH, DELETE, OPTIONS",
		"Access-Control-Max-Age":           "600",
		"Vary":                             "Origin",
	}
	for nome, quer := range cases {
		t.Run(nome, func(t *testing.T) {
			if got := rec.Header().Get(nome); got != quer {
				t.Errorf("%s = %q, queria %q", nome, got, quer)
			}
		})
	}
}

func TestPreflight(t *testing.T) {
	cases := []struct {
		metodo  string
		status  int
		chamado bool
	}{
		{http.MethodOptions, http.StatusNoContent, false},
		{http.MethodGet, http.StatusOK, true},
		{http.MethodPost, http.StatusOK, true},
	}
	for _, c := range cases {
		t.Run(c.metodo, func(t *testing.T) {
			chamado := false
			h := Preflight()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				chamado = true
				proximo(w, r)
			}))
			rec := pedir(h, func(r *http.Request) { r.Method = c.metodo })
			if rec.Code != c.status || chamado != c.chamado {
				t.Errorf("status = %d, chamado = %v; queria %d, %v", rec.Code, chamado, c.status, c.chamado)
			}
		})
	}
}

func TestBodyLimitNoLimiteExato(t *testing.T) {
	const limite = 10
	cases := []struct {
		nome    string
		tamanho int
		cabe    bool
	}{
		{"um byte a menos", limite - 1, true},
		{"exatamente o limite", limite, true},
		{"um byte a mais", limite + 1, false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			var erro error
			h := BodyLimit(limite)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, erro = io.ReadAll(r.Body)
			}))
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", c.tamanho)))
			h.ServeHTTP(httptest.NewRecorder(), r)
			if c.cabe && erro != nil {
				t.Errorf("recusado: %v", erro)
			}
			if !c.cabe && erro == nil {
				t.Error("aceito")
			}
		})
	}
}

func TestAuthnPoeUsuarioEFamiliaNoContexto(t *testing.T) {
	segredo := []byte(strings.Repeat("s", 32))
	usuario := uuid.MustParse("01890000-0000-7000-8000-00000000000a")
	familia := uuid.MustParse("01890000-0000-7000-8000-00000000000b")
	token, err := auth.NewAccessToken(segredo, usuario, familia, instanteBase)
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(instanteBase)
	var gotUsuario, gotFamilia uuid.UUID
	h := Authn(segredo, clk)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUsuario, _ = UserID(r.Context())
		gotFamilia, _ = FamilyID(r.Context())
		proximo(w, r)
	}))
	comToken := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
	if c := pedir(h, comToken).Code; c != http.StatusOK {
		t.Fatalf("status = %d, queria 200", c)
	}
	if gotUsuario != usuario || gotFamilia != familia {
		t.Errorf("usuário = %s, família = %s", gotUsuario, gotFamilia)
	}
}

func TestAuthnRecusaTokenVencidoESemPrefixo(t *testing.T) {
	segredo := []byte(strings.Repeat("s", 32))
	token, err := auth.NewAccessToken(segredo, uuid.New(), uuid.New(), instanteBase)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		nome      string
		avancar   time.Duration
		cabecalho string
		quer      int
	}{
		{"no prazo", time.Minute, "Bearer " + token, http.StatusOK},
		{"vencido", 24 * time.Hour, "Bearer " + token, http.StatusUnauthorized},
		{"sem o prefixo", time.Minute, token, http.StatusUnauthorized},
		{"sem token", time.Minute, "Bearer ", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			clk := clock.NewFake(instanteBase)
			clk.Advance(c.avancar)
			h := Authn(segredo, clk)(http.HandlerFunc(proximo))
			rec := pedir(h, func(r *http.Request) { r.Header.Set("Authorization", c.cabecalho) })
			if rec.Code != c.quer {
				t.Errorf("status = %d, queria %d", rec.Code, c.quer)
			}
		})
	}
}
