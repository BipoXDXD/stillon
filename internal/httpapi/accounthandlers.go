package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

// AccountHandlers são as rotas da conta do dono (Plano 5): GET /me e POST
// /auth/password.
type AccountHandlers struct {
	svc            *account.Service
	trustedProxies []netip.Prefix
}

func NewAccountHandlers(svc *account.Service, trustedProxies []netip.Prefix) *AccountHandlers {
	return &AccountHandlers{svc: svc, trustedProxies: trustedProxies}
}

// meResponse é construído campo a campo: nada de hash, bloqueio ou segredo.
type meResponse struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	TOTPEnabled bool      `json:"totp_enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

// Me diz ao painel quem está logado e se o segundo fator está ligado (é o que
// decide se o formulário de troca de senha pede o código). Usuário que sumiu
// é 401: o painel volta ao login em vez de mostrar erro.
func (h *AccountHandlers) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	a, err := h.svc.Me(r.Context(), userID)
	if errors.Is(err, account.ErrUserNotFound) {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	if err != nil {
		h.internal(w, r, "ler conta", err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, meResponse{
		ID: a.ID.String(), Email: a.Email, TOTPEnabled: a.TOTPEnabled, CreatedAt: a.CreatedAt.UTC(),
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	TOTPCode        string `json:"totp_code"`
}

// ChangePassword é POST /auth/password: 204, e as OUTRAS sessões encerradas.
//
// Os erros seguem o contrato dos Planos 5–7: senha nova fora da política é 400
// com o campo; senha atual, segundo fator ou bloqueio é o 401 genérico do
// login (R34); a sessão do token já encerrada é 401 unauthorized, que o painel
// trata renovando (e a renovação falha, levando ao login).
func (h *AccountHandlers) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !ok || !okF {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	err := h.svc.ChangePassword(r.Context(), userID, familyID, account.ChangePasswordInput{
		Current: req.CurrentPassword, New: req.NewPassword, TOTPCode: req.TOTPCode,
	}, auth.Meta{IP: middleware.ClientIP(r, h.trustedProxies), UserAgent: r.UserAgent()})
	var pe account.PolicyError
	switch {
	case errors.As(err, &pe):
		apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "new_password", Reason: pe.Reason}})
	case errors.Is(err, account.ErrSessionGone):
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	case errors.Is(err, account.ErrInvalidCredentials):
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
	case err != nil:
		h.internal(w, r, "trocar senha", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// internal registra o erro real e devolve só o envelope genérico com o
// trace_id (spec §9.1).
func (h *AccountHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
}
