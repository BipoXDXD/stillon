package httpapi

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/config"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
)

const (
	// pingBodyBytes: o corpo é descartado (D14), e o teto só limita o quanto
	// a drenagem lê.
	pingBodyBytes = 10 << 10
	// pingTimeout fica abaixo do prazo da API: um ping é uma transação curta,
	// e quem o manda é uma tarefa com seu próprio timeout.
	pingTimeout = 5 * time.Second
	// O balde por IP é folgado de propósito: uma máquina com 100 tarefas no
	// mesmo minuto, ou várias atrás de um NAT, manda início e fim de cada uma
	// (200 sinais de uma vez), e o 429 ali viraria alarme falso.
	pingPorIPMinuto, pingPorIPRajada = 1200, 300
	// O balde por token é o que segura um token vazado em laço.
	pingPorTokenMinuto, pingPorTokenRajada = 30, 10
)

// pingOK é fixo: a resposta não diz nada sobre o heartbeat.
var pingOK = struct {
	Status string `json:"status"`
}{Status: "ok"}

// PingHandlers responde às tarefas. Nenhum log deste arquivo leva o caminho
// nem o token: o caminho é a credencial.
type PingHandlers struct {
	svc *heartbeat.PingService
}

func NewPingHandlers(svc *heartbeat.PingService) *PingHandlers {
	return &PingHandlers{svc: svc}
}

func (h *PingHandlers) sinal(sig heartbeat.Signal) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Drena antes do banco: corpo acima do teto não tem efeito nenhum.
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			var grande *http.MaxBytesError
			if errors.As(err, &grande) {
				apierr.WriteError(w, r, apierr.CodePayloadTooLarge)
				return
			}
			apierr.WriteError(w, r, apierr.CodeValidation)
			return
		}
		switch err := h.svc.Ping(r.Context(), r.PathValue("token"), sig); {
		case err == nil:
			apierr.WriteJSON(w, http.StatusOK, pingOK)
		case errors.Is(err, heartbeat.ErrUnknownToken):
			apierr.WriteError(w, r, apierr.CodeNotFound)
		default:
			slog.Error("registrar ping", "erro", err, "trace_id", observability.TraceID(r.Context()))
			apierr.WriteError(w, r, apierr.CodeInternal)
		}
	}
}

// pingPublico é a cadeia de /ping/, separada da API. Quem chama é uma tarefa
// agendada, com curl: fica fora do CORS e do RequireCustomHeader. Sem cookie e
// sem sessão, não há o que um CSRF roubaria. Só POST (D6): uma prévia de link
// ou um scanner que fizesse GET contaria como sinal e esconderia uma tarefa
// parada.
func pingPublico(ctx context.Context, cfg *config.Config, clk clock.Clock, svc *heartbeat.PingService) http.Handler {
	ph := NewPingHandlers(svc)
	porToken := middleware.RateLimitBy(ctx, clk, pingPorTokenMinuto, pingPorTokenRajada, chaveDoToken)
	rota := func(sig heartbeat.Signal) http.Handler { return porToken(ph.sinal(sig)) }

	mux := http.NewServeMux()
	mux.Handle("POST /ping/{token}", rota(heartbeat.SignalSuccess))
	mux.Handle("POST /ping/{token}/start", rota(heartbeat.SignalStart))
	mux.Handle("POST /ping/{token}/fail", rota(heartbeat.SignalFail))

	h := comEnvelope(mux)
	h = middleware.BodyLimit(pingBodyBytes)(h)
	h = middleware.RateLimit(ctx, clk, pingPorIPMinuto, pingPorIPRajada, proxiesDe(cfg))(h)
	h = middleware.SecurityHeaders(h)
	h = middleware.Recover(h)
	h = middleware.Deadline(pingTimeout)(h)
	h = middleware.RequestID(h)
	return h
}

// chaveDoToken é o balde por tarefa, chaveado pelo hash: o limitador guarda
// suas chaves em memória, e o token não fica nela.
func chaveDoToken(r *http.Request) string {
	return hex.EncodeToString(crypto.HashToken(r.PathValue("token")))
}
