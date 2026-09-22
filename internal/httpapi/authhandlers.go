package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
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
	clk          clock.Clock
	jwtSecret    []byte
	secureCookie bool
}

func NewAuthHandlers(users auth.UserStore, sessions *auth.Manager, clk clock.Clock, jwtSecret []byte, secureCookie bool) *AuthHandlers {
	return &AuthHandlers{users: users, sessions: sessions, clk: clk, jwtSecret: jwtSecret, secureCookie: secureCookie}
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

	if user.IsLocked(now) {
		auth.VerifyDummy()
		apierr.WriteError(w, r, apierr.CodeAccountLocked)
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

func (h *AuthHandlers) meta(r *http.Request) auth.Meta {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return auth.Meta{IP: ip, UserAgent: r.UserAgent()}
}

// internal registra o erro real e devolve ao cliente apenas o envelope
// genérico com o trace_id (spec §9.1).
func (h *AuthHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
}
