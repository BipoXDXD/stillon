package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
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

// O log do panic leva o padrão da rota, nunca o caminho: em
// /verify/{token}, o caminho é um token de verificação ainda válido.
func TestRecoverDoesNotLogThePath(t *testing.T) {
	var log bytes.Buffer
	antigo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(antigo) })

	const token = "tok-9f8e7d6c5b4a39281706f5e4d3c2b1a0"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /verify/{token}", func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	h := middleware.RequestID(middleware.Recover(mux))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/verify/"+token, nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, queria 500", rec.Code)
	}
	saida := log.String()
	if !strings.Contains(saida, "panic no handler") {
		t.Fatalf("o panic não foi logado: %q", saida)
	}
	if strings.Contains(saida, token) {
		t.Errorf("o log vazou o token do caminho: %q", saida)
	}
	if !strings.Contains(saida, "/verify/{token}") || !strings.Contains(saida, "metodo=POST") {
		t.Errorf("o log perdeu o padrão da rota ou o método: %q", saida)
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
	h := middleware.RateLimit(ctx, 60, 3, middleware.Proxies{})(http.HandlerFunc(ok))

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
	h := middleware.RateLimit(ctx, 60, 3, middleware.Proxies{})(http.HandlerFunc(ok))

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
	values := []struct {
		name, value      string
		present, allowed bool
	}{
		{name: "ausente"},
		{name: "vazio", present: true},
		{name: "outro", present: true, value: "outro"},
		{name: "caixa diferente", present: true, value: "STILLON"},
		{name: "correto", present: true, value: "stillon", allowed: true},
	}
	for _, group := range []struct {
		methods  []string
		mutation bool
	}{
		{methods: []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut}, mutation: true},
		{methods: []string{http.MethodGet, http.MethodHead, http.MethodOptions}},
	} {
		for _, method := range group.methods {
			for _, value := range values {
				t.Run(method+"/"+value.name, func(t *testing.T) {
					called := false
					h := middleware.RequireCustomHeader(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						called = true
						w.WriteHeader(http.StatusCreated)
					}))
					req := httptest.NewRequest(method, "/", nil)
					if value.present {
						req.Header.Set("x-requested-with", value.value)
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					wantCall := !group.mutation || value.allowed
					wantStatus := http.StatusForbidden
					if wantCall {
						wantStatus = http.StatusCreated
					}
					if rec.Code != wantStatus || called != wantCall {
						t.Fatalf("status%d, next=%t; esperado%d, next=%t", rec.Code, called, wantStatus, wantCall)
					}
					if !wantCall {
						var env struct{ Error struct{ Code string } }
						if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "forbidden" {
							t.Fatalf("recusa sem envelope forbidden: %s; %v", rec.Body.String(), err)
						}
					}
				})
			}
		}
	}
}
