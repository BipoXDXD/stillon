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
func Routes(ctx context.Context, cfg *config.Config, store *auth.PGStore, sessions *auth.Manager, totp *auth.TOTP, clk clock.Clock) http.Handler {
	// Sem https não há como marcar o cookie como Secure: o navegador
	// simplesmente não o guardaria, e o desenvolvimento local pararia. Em
	// produção PUBLIC_BASE_URL é https, e o refresh nunca trafega em claro.
	secureCookie := strings.HasPrefix(cfg.PublicBaseURL, "https://")
	h := NewAuthHandlers(store, sessions, totp, clk, cfg.JWTSecret, secureCookie, cfg.TrustedProxies)
	authn := middleware.Authn(cfg.JWTSecret, clk)

	mux := http.NewServeMux()

	// Rotas de autenticação: limite mais apertado, porque é onde a força
	// bruta bate.
	//
	// Este limitador e o bloqueio progressivo de auth.LockThreshold operam em
	// camadas diferentes e de propósito: o limite é por IP, o bloqueio é por
	// conta. Depois de esgotado o burst ainda passam 10 tentativas por minuto,
	// então cinco falhas na mesma conta chegam em poucos segundos — o
	// bloqueio não fica inalcançável (R35).
	//
	// A ordem tem uma propriedade que vale preservar: o rate limit responde
	// ANTES de o handler rodar, então um atacante remoto é barrado por IP
	// antes de acumular falhas suficientes para trancar a conta de uma vítima.
	// Sem isso, o próprio lockout viraria o ataque — negação de serviço por
	// bloqueio de conta alheia, que é um problema real em sistemas com
	// lockout. Não afrouxe este limitador sem pensar nisso.
	mux.Handle("POST /api/v1/auth/login",
		middleware.RateLimit(ctx, 10, 5, cfg.TrustedProxies)(http.HandlerFunc(h.Login)))
	mux.Handle("POST /api/v1/auth/refresh", http.HandlerFunc(h.Refresh))
	mux.Handle("POST /api/v1/auth/logout", http.HandlerFunc(h.Logout))

	mux.Handle("POST /api/v1/auth/totp/enroll", authn(http.HandlerFunc(h.TOTPEnroll)))
	mux.Handle("POST /api/v1/auth/totp/confirm", authn(http.HandlerFunc(h.TOTPConfirm)))
	// Desativar é a saída que o R38 tirou. Exige senha E posse do fator, no
	// handler — a sessão sozinha aqui seria o bypass de volta.
	mux.Handle("POST /api/v1/auth/totp/disable", authn(http.HandlerFunc(h.TOTPDisable)))
	// Verify não passa por authn: o usuário ainda não tem sessão, só o
	// desafio. O limitador cobre o lugar do bloqueio por conta, que aqui não
	// se aplica — a senha já foi acertada, o que resta é adivinhar seis
	// dígitos.
	mux.Handle("POST /api/v1/auth/totp/verify",
		middleware.RateLimit(ctx, 20, 5, cfg.TrustedProxies)(http.HandlerFunc(h.TOTPVerify)))

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
	// Preflight fica DENTRO do rate limit e FORA do RequireCustomHeader: o
	// OPTIONS passa a ser contado pelo limitador (item 24) sem precisar do
	// cabeçalho customizado, que nenhum preflight carrega. Os cabeçalhos de
	// CORS continuam por fora do limitador, em middleware.CORS, para que o
	// 429 também os leve (R33).
	handler = middleware.Preflight()(handler)
	handler = middleware.RateLimit(ctx, 120, 30, cfg.TrustedProxies)(handler)
	handler = middleware.CORS(cfg.CORSOrigin)(handler)
	handler = middleware.SecurityHeaders(handler)
	handler = middleware.Recover(handler)
	handler = middleware.RequestID(handler)

	return handler
}
