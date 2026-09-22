package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/sentinela/internal/apierr"
	"github.com/BipoXDXD/sentinela/internal/auth"
	"github.com/BipoXDXD/sentinela/internal/httpapi/middleware"
)

type codeRequest struct {
	Code string `json:"code"`
}

// TOTPEnroll exige sessão ativa: cadastrar segundo fator é operação de conta
// já autenticada.
func (h *AuthHandlers) TOTPEnroll(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	user, err := h.users.ByID(r.Context(), userID)
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}

	uri, err := h.totp.Enroll(r.Context(), userID, user.Email)
	if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
		// R38: recadastrar não pode ser a porta dos fundos para desligar o
		// segundo fator de quem já o tem ativo.
		apierr.WriteError(w, r, apierr.CodeForbidden)
		return
	}
	if err != nil {
		h.internal(w, r, "cadastrar TOTP", err)
		return
	}
	// A URI otpauth:// vai para o cliente, que renderiza o QR code. Gerar a
	// imagem no servidor só acrescentaria payload.
	apierr.WriteJSON(w, http.StatusOK, map[string]string{"otpauth_uri": uri})
}

func (h *AuthHandlers) TOTPConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	var req codeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}

	codes, err := h.totp.Confirm(r.Context(), userID, req.Code, h.clk.Now())
	if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}
	if err != nil {
		h.internal(w, r, "confirmar TOTP", err)
		return
	}

	// Única vez em que os códigos aparecem em claro.
	apierr.WriteJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// TOTPVerify fecha o login em dois passos: consome o desafio e emite a sessão.
func (h *AuthHandlers) TOTPVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}
	now := h.clk.Now()

	userID, err := auth.ParseChallengeToken(h.jwtSecret, req.Challenge, now)
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	// O segundo fator entra no MESMO bloqueio progressivo do login (R40).
	// Sem isto, quem já tem a senha chuta seis dígitos indefinidamente: o
	// limite de /totp/verify é por IP, e um atacante com várias origens passa
	// por cima dele sem que a conta jamais tranque.
	user, err := h.users.ByID(r.Context(), userID)
	if errors.Is(err, auth.ErrNotFound) {
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}
	if user.IsLocked(now) {
		// Mesmo 401 genérico do login, pelo mesmo motivo do R34.
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}

	if err := h.totp.Verify(r.Context(), userID, req.Code, now); err != nil {
		// O mesmo 401 genérico do login errado (R34): um código distinto para
		// "segundo fator falhou" contaria ao atacante que a senha estava
		// certa, e a senha é justamente o que ele está testando.
		if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
			if err := h.users.RecordFailure(r.Context(), userID, now); err != nil {
				slog.Error("registrar falha de segundo fator", "erro", err)
			}
			apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
			return
		}
		h.internal(w, r, "verificar TOTP", err)
		return
	}

	if err := h.users.ResetFailures(r.Context(), userID); err != nil {
		slog.Error("zerar falhas de segundo fator", "erro", err)
	}

	h.issue(w, r, userID)
}
