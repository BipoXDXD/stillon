package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

var chaveIPDeTeste = []byte("chave-de-ip-sintetica-so-para-teste-32b")

// orcamentoGravado responde o que o teste manda e guarda o que recebeu.
type orcamentoGravado struct {
	espera    time.Duration
	permitido bool
	err       error

	balde  string
	chaves [][]byte
	aCada  time.Duration
	rajada int
}

func (o *orcamentoGravado) Gastar(_ context.Context, balde string, chave []byte, aCada time.Duration, rajada int) (time.Duration, bool, error) {
	o.balde, o.aCada, o.rajada = balde, aCada, rajada
	o.chaves = append(o.chaves, chave)
	return o.espera, o.permitido, o.err
}

var baldeDeTeste = middleware.BaldeIP{Nome: "login", PorMinuto: 10, Rajada: 5}

func pedirComOrcamento(o middleware.Orcamentos, remoto string) (*httptest.ResponseRecorder, bool) {
	chamado := false
	h := middleware.RateLimitIPCompartilhado(o, baldeDeTeste, middleware.Proxies{}, chaveIPDeTeste)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { chamado = true; w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.RemoteAddr = remoto
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, chamado
}

func TestSharedIPLimitPassesTheBucketRule(t *testing.T) {
	o := &orcamentoGravado{permitido: true}

	rec, chamado := pedirComOrcamento(o, "203.0.113.9:1234")

	if !chamado || rec.Code != http.StatusNoContent {
		t.Fatalf("permitido: %d, handler chamado = %v", rec.Code, chamado)
	}
	if o.balde != "login" || o.aCada != 6*time.Second || o.rajada != 5 {
		t.Errorf("regra recebida = (%q, %v, %d), queria (login, 6s, 5)", o.balde, o.aCada, o.rajada)
	}
}

// O orçamento recebe o HMAC da origem, e não o IP: é o que vai para o banco.
// Endereços do mesmo /64 caem na mesma chave.
func TestSharedIPLimitKeysByTheKeyedHashOfTheOrigin(t *testing.T) {
	o := &orcamentoGravado{permitido: true}

	pedirComOrcamento(o, "203.0.113.9:1234")
	pedirComOrcamento(o, "[2001:db8:1:2::a]:1234")
	pedirComOrcamento(o, "[2001:db8:1:2::b]:1234")

	if want := crypto.HashIP(chaveIPDeTeste, "203.0.113.9"); !bytes.Equal(o.chaves[0], want) {
		t.Errorf("chave do IPv4 = %x, queria o HMAC da origem %x", o.chaves[0], want)
	}
	if bytes.Contains(o.chaves[0], []byte("203.0.113.9")) {
		t.Error("o IP foi para o orçamento em claro")
	}
	if !bytes.Equal(o.chaves[1], o.chaves[2]) {
		t.Error("dois endereços do mesmo /64 ganharam chaves diferentes")
	}
}

func TestSharedIPLimitRefusesWithRetryAfterRoundedUp(t *testing.T) {
	o := &orcamentoGravado{espera: 2100 * time.Millisecond}

	rec, chamado := pedirComOrcamento(o, "203.0.113.9:1234")

	if chamado {
		t.Error("o handler rodou com o balde vazio")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("recusado: %d, queria 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Errorf("Retry-After = %q, queria 3", got)
	}
	if !strings.Contains(rec.Body.String(), `"rate_limited"`) {
		t.Errorf("corpo sem rate_limited: %s", rec.Body.String())
	}
}

// Sem resposta do orçamento, a requisição para (falha fechado), com o erro
// interno genérico, e o log não leva o IP.
func TestSharedIPLimitFailsClosedWithoutLoggingTheIP(t *testing.T) {
	var log bytes.Buffer
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(anterior) })
	o := &orcamentoGravado{err: errors.New("banco fora")}

	rec, chamado := pedirComOrcamento(o, "203.0.113.9:1234")

	if chamado {
		t.Error("o handler rodou sem resposta do orçamento")
	}
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"internal_error"`) {
		t.Errorf("sem orçamento: %d %s, queria 500 internal_error", rec.Code, rec.Body.String())
	}
	if !strings.Contains(log.String(), "banco fora") {
		t.Errorf("o erro não foi para o log: %s", log.String())
	}
	if strings.Contains(log.String(), "203.0.113.9") || strings.Contains(rec.Body.String(), "203.0.113.9") {
		t.Error("o IP vazou no log ou na resposta")
	}
}

// A versão em memória é de um processo só: duas instâncias não dividem nada.
// A mesma instância divide o balde entre handlers.
func TestInMemoryBudgetsShareOnlyWithinOneInstance(t *testing.T) {
	ctx := t.Context()
	uma := middleware.NovosOrcamentosEmMemoria(ctx, relogioParado())
	for range baldeDeTeste.Rajada {
		pedirComOrcamento(uma, "203.0.113.9:1234")
	}
	if rec, _ := pedirComOrcamento(uma, "203.0.113.9:1234"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a mesma instância além da rajada: %d, queria 429", rec.Code)
	}
	if rec, _ := pedirComOrcamento(middleware.NovosOrcamentosEmMemoria(ctx, relogioParado()), "203.0.113.9:1234"); rec.Code != http.StatusNoContent {
		t.Errorf("outra instância: %d, queria 204", rec.Code)
	}
}

func relogioParado() *clock.Fake {
	return clock.NewFake(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
}
