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

// TOTPEnroll exige sessão ativa E prova de posse da senha.
//
// A sessão sozinha não basta, e o motivo é o espelho exato do R38. Aquela
// ruling fechou "token roubado DESLIGA o segundo fator"; faltava fechar
// "token roubado LIGA o segundo fator com o autenticador do ladrão", que é
// pior por três razões: o R38 removeu o caminho de volta, então o sequestro
// não se desfaz por dentro; os códigos de recuperação saem no mesmo passo,
// para o atacante; e ele persiste depois que a sessão roubada expira,
// enquanto o dono perde o acesso. A senha é o que o ladrão de sessão não tem.
func (h *AuthHandlers) TOTPEnroll(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	var req passwordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}

	user, err := h.users.ByID(r.Context(), userID)
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}

	if !h.senhaConfere(w, r, user, req.Password) {
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
	if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
		// Mesmo 403 do enroll: reconfirmar rotacionaria os códigos de
		// recuperação e apagaria a data de ativação original.
		apierr.WriteError(w, r, apierr.CodeForbidden)
		return
	}
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

// TOTPVerify fecha o login em dois passos: valida o desafio e emite a sessão.
//
// O desafio NÃO é consumido — é um JWT sem jti, e revogá-lo exigiria estado
// que este plano não tem. O comentário anterior dizia que ele era consumido,
// e isso era falso. O que sobra depois do item 11 (consumo do timestep,
// RFC 6238 §5.2) é estreito: com o desafio de cinco minutos em mãos, cada
// sessão adicional exige um código NOVO do autenticador, porque o já usado
// não entra duas vezes. O R40 limita as tentativas erradas a LockThreshold.
// O resíduo é sessão paralela para quem já provou senha e segundo fator —
// higiene, não escalada. Fechar de vez pede jti em denylist ou um
// challenge_id consumido no verify; fica nomeado para o Plano 5.
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

type passwordRequest struct {
	Password string `json:"password"`
}

// senhaConfere escreve o 401 e devolve false quando a senha não bate. O erro
// é o mesmo CodeInvalidCredentials do login, pelo motivo do R34: um código
// distinto aqui contaria ao atacante que a sessão que ele roubou é válida e
// que só falta a senha.
func (h *AuthHandlers) senhaConfere(w http.ResponseWriter, r *http.Request, user auth.User, senha string) bool {
	ok, _, err := auth.VerifyPassword(user.PasswordHash, senha)
	if err != nil {
		h.internal(w, r, "verificar senha", err)
		return false
	}
	if !ok {
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return false
	}
	return true
}

// TOTPDisable exige DUAS provas: a senha e a posse do fator.
//
// Só a senha rebaixaria o 2FA a nada — quem descobrisse a senha desligaria o
// segundo fator sozinho. Só o código reabriria o bypass que o R38 fechou, em
// que uma sessão roubada bastava. O código de recuperação vale no lugar do
// código do app, senão quem perde o telefone fica sem saída, que foi
// justamente a lacuna que o R38 deixou.
func (h *AuthHandlers) TOTPDisable(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	var req struct {
		Password     string `json:"password"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}

	user, err := h.users.ByID(r.Context(), userID)
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}
	if !h.senhaConfere(w, r, user, req.Password) {
		return
	}

	prova := req.Code
	if prova == "" {
		prova = req.RecoveryCode
	}
	if prova == "" {
		// Sem a segunda prova o pedido não chega ao serviço: a senha sozinha
		// nunca desliga o fator.
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	err = h.totp.Disable(r.Context(), userID, prova, h.clk.Now())
	if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	if err != nil {
		h.internal(w, r, "desativar TOTP", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
