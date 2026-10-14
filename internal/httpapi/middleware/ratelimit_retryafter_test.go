package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

// O 429 diz quando tentar de novo, em segundos inteiros: com 10 por minuto, o
// próximo token chega em até 6 s.
func TestRateLimitAnnouncesRetryAfter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := middleware.RateLimit(ctx, 10, 2, middleware.Proxies{})(http.HandlerFunc(ok))

	var rec *httptest.ResponseRecorder
	for range 3 {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a terceira requisição deu %d, queria 429", rec.Code)
	}
	segundos, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || segundos < 1 || segundos > 6 {
		t.Errorf("Retry-After = %q, queria um inteiro entre 1 e 6", rec.Header().Get("Retry-After"))
	}
}

// O balde por usuário de envio (um a cada 6 minutos) anuncia o prazo dele.
func TestRateLimitUserEveryAnnouncesItsOwnRetryAfter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agora := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	chave := []byte("chave-jwt-sintetica-publica-somente-para-teste")
	token, err := auth.NewAccessToken(chave, uuid.New(), uuid.New(), agora)
	if err != nil {
		t.Fatal(err)
	}
	limitado := middleware.RateLimitUserEvery(ctx, 6*time.Minute, 1)(http.HandlerFunc(ok))
	h := middleware.Authn(chave, clock.NewFake(agora))(limitado)

	var rec *httptest.ResponseRecorder
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("o segundo envio deu %d, queria 429", rec.Code)
	}
	segundos, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || segundos < 355 || segundos > 360 {
		t.Errorf("Retry-After = %q, queria perto de 360", rec.Header().Get("Retry-After"))
	}
}

// A recusa não gasta token: se gastasse, cada 429 empurraria o próximo token
// para mais longe, e o cliente que insiste veria o Retry-After crescer sem
// fim (e o legítimo atrás do mesmo IP pagaria por ele). Depois de dez
// recusas seguidas, o prazo continua o de um token só.
func TestRateLimitRefusalDoesNotSpendTokens(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := middleware.RateLimit(ctx, 10, 2, middleware.Proxies{})(http.HandlerFunc(ok))

	var rec *httptest.ResponseRecorder
	for range 12 {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a décima segunda requisição deu %d, queria 429", rec.Code)
	}
	segundos, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || segundos < 1 || segundos > 6 {
		t.Errorf("Retry-After depois de dez recusas = %q, queria um inteiro entre 1 e 6", rec.Header().Get("Retry-After"))
	}
}
