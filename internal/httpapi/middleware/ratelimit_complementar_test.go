package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

func exigirErroRateLimitComplementar(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, queria %d; body=%s", rec.Code, status, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != code {
		t.Fatalf("envelope = %s, erro = %v; queria código %q", rec.Body.String(), err, code)
	}
}

// A origem opaca representa conexões fora do HTTP/TCP usual, como listener
// unix. Não recomenda taxa zero em produção: protege o contrato da API Go.
func TestRateLimitZeroKeepsOpaqueOriginsSeparatedComplementar(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) })
	handler := middleware.RateLimit(ctx, clock.Real{}, 0, 2, middleware.Proxies{})(next)
	steps := []struct {
		origin, forged string
		status, calls  int
	}{
		{"@primeiro", "203.0.113.10", http.StatusNoContent, 1},
		{"@primeiro", "203.0.113.11", http.StatusNoContent, 2},
		{"@primeiro", "203.0.113.12", http.StatusTooManyRequests, 2},
		{"@segundo", "203.0.113.13", http.StatusNoContent, 3},
		{"@segundo", "203.0.113.14", http.StatusNoContent, 4},
		{"@segundo", "203.0.113.15", http.StatusTooManyRequests, 4},
		{"@primeiro", "", http.StatusTooManyRequests, 4},
	}
	for index, step := range steps {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		request.RemoteAddr = step.origin
		request.Header.Set("X-Forwarded-For", step.forged)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request)
		if rec.Code != step.status || calls != step.calls {
			t.Fatalf("requisição %d: status=%d, chamadas=%d; queria %d/%d", index+1, rec.Code, calls, step.status, step.calls)
		}
		if step.status == http.StatusTooManyRequests {
			exigirErroRateLimitComplementar(t, rec, http.StatusTooManyRequests, "rate_limited")
		}
	}
}

// Every <= 0 representa Inf na API de rate; isso difere de taxa zero.
// Authn cria a identidade real do caller, sem inserir chave privada de contexto.
func TestRateLimitUserEveryNonpositiveUsesInfinityComplementar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		every time.Duration
	}{{"zero", 0}, {"negativo", -time.Second}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
			clk := clock.NewFake(now)
			key := []byte("chave-jwt-sintetica-publica-somente-para-teste")
			user := uuid.MustParse("00000000-0000-4000-8000-000000000001")
			family := uuid.MustParse("00000000-0000-4000-8000-000000000002")
			token, err := auth.NewAccessToken(key, user, family, now)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, ok := middleware.UserID(r.Context())
				if !ok || got != user {
					t.Errorf("identidade autenticada = %v/%v, queria %v", got, ok, user)
				}
				calls++
				w.WriteHeader(http.StatusNoContent)
			})
			handler := middleware.Authn(key, clk)(middleware.RateLimitUserEvery(ctx, clock.Real{}, tc.every, 0)(next))
			send := func(bearer string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(http.MethodPost, "/api/v1/channels", nil)
				request.RemoteAddr = "192.0.2.10:1234"
				if bearer != "" {
					request.Header.Set("Authorization", "Bearer "+bearer)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, request)
				return rec
			}
			exigirErroRateLimitComplementar(t, send(""), http.StatusUnauthorized, "unauthorized")
			if calls != 0 {
				t.Fatal("requisição sem token chegou ao handler")
			}
			for index := range 5 {
				rec := send(token)
				if rec.Code != http.StatusNoContent || calls != index+1 {
					t.Fatalf("Inf com burst zero: status=%d, chamadas=%d; queria 204/%d", rec.Code, calls, index+1)
				}
			}
			exigirErroRateLimitComplementar(t, send(""), http.StatusUnauthorized, "unauthorized")
			if calls != 5 {
				t.Fatal("Inf contornou Authn")
			}
		})
	}
}
