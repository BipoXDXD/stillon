package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/config"
	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
)

// maxBodyBytes cobre com folga qualquer payload legítimo desta API.
const maxBodyBytes = 1 << 20 // 1 MiB

// Routes monta a árvore de rotas e a cadeia de middlewares.
//
// ctx é o contexto de vida do servidor: ele encerra as goroutines de
// varredura dos rate limiters no shutdown, em vez de deixá-las vazar (R32).
func Routes(ctx context.Context, cfg *config.Config, store *auth.PGStore, sessions *auth.Manager, clk clock.Clock) http.Handler {
	// Sem https não há como marcar o cookie como Secure: o navegador
	// simplesmente não o guardaria, e o desenvolvimento local pararia. Em
	// produção PUBLIC_BASE_URL é https, e o refresh nunca trafega em claro.
	secureCookie := strings.HasPrefix(cfg.PublicBaseURL, "https://")
	h := NewAuthHandlers(store, sessions, clk, cfg.JWTSecret, secureCookie)
	authn := middleware.Authn(cfg.JWTSecret, clk)

	mux := http.NewServeMux()

	// Rotas de autenticação: limite mais apertado, porque é onde a força
	// bruta bate.
	mux.Handle("POST /api/v1/auth/login",
		middleware.RateLimit(ctx, 10, 5)(http.HandlerFunc(h.Login)))
	mux.Handle("POST /api/v1/auth/refresh", http.HandlerFunc(h.Refresh))
	mux.Handle("POST /api/v1/auth/logout", http.HandlerFunc(h.Logout))

	mux.Handle("GET /api/v1/me", authn(http.HandlerFunc(h.Me)))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// A ordem importa, e se escreve de dentro para fora: a ÚLTIMA linha é o
	// middleware mais externo.
	//
	// RequestID tem de ser o mais externo de todos. apierr.WriteError lê o
	// trace do contexto, e o contexto só o tem depois que RequestID rodou —
	// um erro produzido por qualquer middleware acima dele sairia com
	// trace_id vazio, impossível de correlacionar com o log. Recover vem
	// logo em seguida, para capturar panic de todo o resto da cadeia.
	var handler http.Handler = mux
	handler = middleware.RequireCustomHeader(handler)
	handler = middleware.BodyLimit(maxBodyBytes)(handler)
	handler = middleware.RateLimit(ctx, 120, 30)(handler)
	handler = middleware.CORS(cfg.CORSOrigin)(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(handler)
	handler = middleware.RequestID(handler)

	return handler
}
