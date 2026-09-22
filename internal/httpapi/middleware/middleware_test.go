package middleware_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
)

func ok(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

func TestSecurityHeadersArePresent(t *testing.T) {
	h := middleware.SecurityHeaders(http.HandlerFunc(ok))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, queria %q", k, got, v)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP sem frame-ancestors: %q", csp)
	}
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "unsafe-inline") {
		t.Error("a CSP não pode conter unsafe-inline (spec §5.7)")
	}
}

// Um panic não pode nunca entregar stack trace ao cliente: o stack revela
// caminhos de arquivo, nomes de pacote e versões.
func TestRecoverHidesTheStackTrace(t *testing.T) {
	boom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("segredo-no-panic")
	})
	h := middleware.RequestID(middleware.Recover(boom))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, queria 500", rec.Code)
	}
	body := rec.Body.String()
	for _, proibido := range []string{"segredo-no-panic", "goroutine", ".go:"} {
		if strings.Contains(body, proibido) {
			t.Errorf("a resposta vazou %q: %s", proibido, body)
		}
	}

	var env struct {
		Error   struct{ Code, Message string } `json:"error"`
		TraceID string                         `json:"trace_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("resposta não é o envelope esperado: %v", err)
	}
	if env.Error.Code != "internal_error" {
		t.Errorf("code = %q, queria internal_error", env.Error.Code)
	}
	if env.TraceID == "" {
		t.Error("sem trace_id: o erro fica impossível de correlacionar no log")
	}
}

func TestBodyLimitRejectsOversizedPayload(t *testing.T) {
	h := middleware.BodyLimit(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 500)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, queria 413", rec.Code)
	}
}

func TestRateLimitBlocksAfterBurst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := middleware.RateLimit(ctx, 60, 3)(http.HandlerFunc(ok))

	var last int
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("status depois de 10 requisições = %d, queria 429", last)
	}
}

func TestRateLimitIsPerClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := middleware.RateLimit(ctx, 60, 3)(http.HandlerFunc(ok))

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	outro := httptest.NewRequest(http.MethodGet, "/", nil)
	outro.RemoteAddr = "198.51.100.7:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, outro)

	if rec.Code != http.StatusOK {
		t.Errorf("outro cliente recebeu %d; o limite não é por cliente", rec.Code)
	}
}

func TestCORSOnlyEchoesTheConfiguredOrigin(t *testing.T) {
	h := middleware.CORS("https://app.exemplo.com")(http.HandlerFunc(ok))

	permitida := httptest.NewRequest(http.MethodGet, "/", nil)
	permitida.Header.Set("Origin", "https://app.exemplo.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, permitida)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.exemplo.com" {
		t.Errorf("origem permitida devolveu %q", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, queria Origin (origem permitida)", got)
	}

	hostil := httptest.NewRequest(http.MethodGet, "/", nil)
	hostil.Header.Set("Origin", "https://atacante.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, hostil)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("origem não permitida recebeu %q; deveria receber vazio", got)
	}
	// A resposta também depende do header Origin nesse ramo: sem Vary aqui,
	// um cache poderia servir a resposta desta requisição hostil (sem
	// headers de CORS) para um cliente legítimo depois (R33).
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, queria Origin (origem hostil)", got)
	}
}

// Segunda camada anti-CSRF (spec §5.3): um formulário HTML cross-site não
// consegue definir header customizado.
func TestRequireCustomHeaderOnMutations(t *testing.T) {
	h := middleware.RequireCustomHeader(http.HandlerFunc(ok))

	sem := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sem)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST sem o header devolveu %d, queria 403", rec.Code)
	}

	com := httptest.NewRequest(http.MethodPost, "/", nil)
	com.Header.Set("X-Requested-With", "sentinela")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, com)
	if rec.Code != http.StatusOK {
		t.Errorf("POST com o header devolveu %d, queria 200", rec.Code)
	}

	leitura := httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, leitura)
	if rec.Code != http.StatusOK {
		t.Error("GET não deveria exigir o header")
	}
}
