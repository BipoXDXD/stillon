package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/apierr"
	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/clock"
	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
	"github.com/BipoXDXD/sentinela/internal/observability"
)

const refreshCookieName = "sentinela_refresh"

type AuthHandlers struct {
	users        auth.UserStore
	sessions     *auth.Manager
	totp         *auth.TOTP
	clk          clock.Clock
	jwtSecret    []byte
	secureCookie bool
	// trustedProxies decide se o X-Forwarded-For desta requisição vale. Ver
	// middleware.ClientIP: sem a lista, acreditar no cabeçalho seria pior que
	// ignorá-lo.
	trustedProxies []netip.Prefix
}

func NewAuthHandlers(users auth.UserStore, sessions *auth.Manager, totp *auth.TOTP, clk clock.Clock, jwtSecret []byte, secureCookie bool, trustedProxies []netip.Prefix) *AuthHandlers {
	return &AuthHandlers{users: users, sessions: sessions, totp: totp, clk: clk, jwtSecret: jwtSecret, secureCookie: secureCookie, trustedProxies: trustedProxies}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// Login faz o mesmo trabalho de CPU exista ou não a conta.
//
// Quando o e-mail não existe, VerifyDummy roda um Argon2id completo. Sem
// isso, a resposta para e-mail inexistente voltaria em microssegundos e a
// resposta para senha errada em ~100 ms, e a diferença enumeraria as contas
// do sistema (spec §5.2). O caminho de conta bloqueada paga o mesmo custo
// pela mesma razão.
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}
	now := h.clk.Now()

	user, err := h.users.ByEmail(r.Context(), req.Email)
	if errors.Is(err, auth.ErrNotFound) {
		auth.VerifyDummy()
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}

	// O bloqueio não se anuncia (R34, spec §5.2): a resposta é o mesmo 401
	// genérico de senha errada e de e-mail inexistente.
	//
	// Uma mensagem distinta aqui reintroduziria exatamente a enumeração que o
	// VerifyDummy existe para impedir, e por um canal muito mais barato que o
	// tempo de CPU: bastaria mandar seis senhas erradas por e-mail e olhar se
	// a resposta muda. Onde mudasse, a conta existe. Todo o cuidado com o
	// custo de Argon2 seria em vão.
	//
	// O preço é de usabilidade — quem está bloqueado não recebe explicação.
	// A spec escolheu esse lado: o bloqueio dura 15 minutos e o evento fica no
	// audit log, que é onde essa informação pertence. Não "melhore a UX" aqui
	// sem reabrir o buraco.
	if user.IsLocked(now) {
		// O custo de CPU continua igualado: esta também é uma falha de login.
		auth.VerifyDummy()
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}

	ok, needsRehash, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		h.internal(w, r, "verificar senha", err)
		return
	}
	if !ok {
		if err := h.users.RecordFailure(r.Context(), user.ID, now); err != nil {
			slog.Error("registrar falha de login", "erro", err)
		}
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}

	if needsRehash {
		if novo, err := auth.HashPassword(req.Password); err == nil {
			if err := h.users.UpdatePasswordHash(r.Context(), user.ID, novo); err != nil {
				slog.Error("regravar hash de senha", "erro", err)
			}
		}
	}

	// Com segundo fator ativo, a senha correta sozinha não abre sessão
	// nenhuma: o que sai daqui é um desafio de vida curta, com audience
	// própria, que ParseAccessToken recusa como credencial.
	enabled, err := h.totp.IsEnabled(r.Context(), user.ID)
	if err != nil {
		h.internal(w, r, "consultar segundo fator", err)
		return
	}
	if enabled {
		challenge, err := auth.NewChallengeToken(h.jwtSecret, user.ID, now)
		if err != nil {
			h.internal(w, r, "assinar desafio", err)
			return
		}
		// Nenhum cookie de refresh é emitido aqui: a sessão só nasce depois
		// do segundo fator.
		apierr.WriteJSON(w, http.StatusOK, map[string]any{
			"totp_required": true,
			"challenge":     challenge,
		})
		return
	}

	// O contador de falhas só zera quando o login de fato termina. Com
	// segundo fator ativo ele termina em /totp/verify, e é lá que o reset
	// acontece: zerar aqui daria ao atacante que já tem a senha um botão de
	// destravar entre um chute de seis dígitos e o próximo, e o bloqueio do
	// R40 nunca chegaria ao limiar.
	if err := h.users.ResetFailures(r.Context(), user.ID); err != nil {
		slog.Error("zerar falhas de login", "erro", err)
	}

	h.issue(w, r, user.ID)
}

func (h *AuthHandlers) Refresh(w http.ResponseWriter, r *http.Request) {
	ck, err := r.Cookie(refreshCookieName)
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeInvalidSession)
		return
	}

	token, s, err := h.sessions.Rotate(r.Context(), ck.Value, h.meta(r))
	switch {
	case errors.Is(err, auth.ErrTokenReuse):
		// A família já foi revogada dentro de Rotate. Limpar o cookie evita
		// que o navegador continue reenviando um token morto.
		slog.Warn("reúso de refresh token detectado",
			"trace_id", observability.TraceID(r.Context()))
		h.clearCookie(w)
		apierr.WriteError(w, r, apierr.CodeInvalidSession)
		return
	case errors.Is(err, auth.ErrInvalidSession):
		h.clearCookie(w)
		apierr.WriteError(w, r, apierr.CodeInvalidSession)
		return
	case err != nil:
		h.internal(w, r, "rotacionar sessão", err)
		return
	}

	h.setCookie(w, token, s.ExpiresAt)
	h.writeAccess(w, r, s.UserID)
}

func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.sessions.Revoke(r.Context(), ck.Value); err != nil {
			slog.Error("revogar sessão", "erro", err)
		}
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandlers) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]string{"id": userID.String()})
}

func (h *AuthHandlers) issue(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	token, s, err := h.sessions.Issue(r.Context(), userID, h.meta(r))
	if err != nil {
		h.internal(w, r, "emitir sessão", err)
		return
	}
	h.setCookie(w, token, s.ExpiresAt)
	h.writeAccess(w, r, userID)
}

func (h *AuthHandlers) writeAccess(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	access, err := auth.NewAccessToken(h.jwtSecret, userID, h.clk.Now())
	if err != nil {
		h.internal(w, r, "assinar access token", err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, tokenResponse{
		AccessToken: access,
		ExpiresIn:   int(auth.AccessTTL.Seconds()),
	})
}

func (h *AuthHandlers) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:  refreshCookieName,
		Value: token,
		// Path restrito: o cookie só acompanha as rotas de autenticação,
		// então não é enviado em nenhuma outra requisição da aplicação.
		Path:     "/api/v1/auth",
		Expires:  expires,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandlers) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

// meta usa o MESMO endereço que o limitador, e não o RemoteAddr cru. A §4.2
// criou ip_hash para correlacionar tentativas da mesma origem; atrás de um
// proxy, o RemoteAddr é o proxy e a coluna passa a correlacionar nada. Ao
// contrário do rate limit, esse dano não tem volta: linha já gravada com o
// hash errado não se recupera.
func (h *AuthHandlers) meta(r *http.Request) auth.Meta {
	return auth.Meta{IP: middleware.ClientIP(r, h.trustedProxies), UserAgent: r.UserAgent()}
}

// internal registra o erro real e devolve ao cliente apenas o envelope
// genérico com o trace_id (spec §9.1).
func (h *AuthHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
}
