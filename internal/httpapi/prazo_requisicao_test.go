package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/httpapi"
	"github.com/BipoXDXD/stillon/internal/notify"
	"github.com/BipoXDXD/stillon/internal/statuspage"
)

// rotasSemBanco monta Routes sem Postgres: o /readyz só chama o ping
// injetado, e o ping vê o contexto que a cadeia de middlewares lhe deu.
func rotasSemBanco(t *testing.T, cfg *config.Config, ping func(context.Context) error) http.Handler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	clk := clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC))
	return httpapi.Routes(ctx, cfg, httpapi.Deps{
		Channels:    notify.NewChannelService(nil, nil, clk, nil, nil, cfg.PublicBaseURL),
		StatusPages: statuspage.NewService(nil, clk),
		Clock:       clk,
		Ready:       ping,
	})
}

func configSemBanco(prazo time.Duration) *config.Config {
	return &config.Config{
		CORSOrigin:     "https://app.exemplo.test",
		JWTSecret:      []byte("segredo-jwt-sintetico-so-para-teste"),
		IPHashKey:      []byte("chave-de-ip-sintetica-so-para-teste"),
		MasterKey:      []byte("chave-mestra-de-teste-32-bytes!!"),
		RequestTimeout: prazo,
	}
}

// O /readyz limita o ping a 2 s, mas um prazo de requisição menor vence:
// com 50 ms na configuração, o ping não pode ganhar mais que isso. Com o
// prazo fixo no código, ele ganharia os 2 s do readyz.
func TestRoutesAplicaOPrazoDaConfiguracao(t *testing.T) {
	var restante time.Duration
	rotas := rotasSemBanco(t, configSemBanco(50*time.Millisecond), func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("ping sem prazo")
			return nil
		}
		restante = time.Until(deadline)
		return nil
	})
	w := httptest.NewRecorder()
	rotas.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK || restante <= 0 || restante > 50*time.Millisecond {
		t.Fatalf("status=%d restante=%v", w.Code, restante)
	}
}

// Um Config montado sem prazo deixaria toda requisição vencida na largada:
// é defeito do código, e Routes recusa na montagem em vez de servir 503.
func TestRoutesRecusaConfigSemPrazo(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Routes aceitou Config sem RequestTimeout")
		}
	}()
	rotasSemBanco(t, configSemBanco(0), nil)
}
