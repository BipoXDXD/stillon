package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
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
	if !decodeJSON(w, r, &req) {
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
	if !decodeJSON(w, r, &req) {
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
// O desafio vale uma vez (Plano 8). É um JWT sem estado próprio; o que o torna
// de uso único é a marca d'água em users.totp_challenge_mark: o desafio só
// abre sessão se for mais novo que a marca, e abri-la a avança até o instante
// dele. Antes, o mesmo desafio abria uma sessão por código TOTP novo durante
// os cinco minutos de validade. Efeito colateral aceito: consumir um desafio
// invalida os mais antigos do mesmo usuário ainda abertos (dois logins quase
// simultâneos: se o mais novo terminar primeiro, o mais antigo refaz a senha).
//
// Ordem: desafio → usuário/bloqueio → pré-checagem da marca → código →
// ResetFailures → ConsumeChallenge → sessão. A pré-checagem recusa antes do
// código o desafio que já não pode abrir sessão, para não gastar um código de
// recuperação com ele (revisão final do Plano 8).
func (h *AuthHandlers) TOTPVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	now := h.clk.Now()

	desafio, err := auth.ParseChallengeToken(h.jwtSecret, req.Challenge, now)
	if err != nil {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	userID := desafio.UserID

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

	// Pré-checagem da marca, ANTES do código: um desafio já consumido, ou mais
	// antigo que um consumido, não abre sessão de jeito nenhum, e verificar o
	// código com ele só custaria caro — gastaria um código de recuperação (que
	// não se renova e o TOTP.Verify apaga numa transação própria), o timestep
	// e zeraria o contador de falhas, tudo para devolver 401. Não conta no
	// bloqueio: não houve código errado. É o mesmo 401 unauthorized do
	// desafio vencido.
	//
	// NÃO é atômica: dois verify simultâneos com o mesmo desafio passam os
	// dois por aqui. O ConsumeChallenge depois do código continua sendo a
	// autoridade, e nessa corrida o perdedor ainda pode gastar um código
	// (resíduo registrado nas pendências).
	if user.TOTPChallengeMark != nil && !desafio.IssuedAt.After(*user.TOTPChallengeMark) {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
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

	// O consumo vem DEPOIS do código, de propósito: consumido antes, um
	// código digitado errado queimaria o desafio e obrigaria a digitar a
	// senha de novo. Desafio já usado é o mesmo 401 unauthorized de um
	// desafio vencido, e não conta no bloqueio: quem chega aqui acertou o
	// código.
	consumido, err := h.users.ConsumeChallenge(r.Context(), userID, desafio.IssuedAt)
	if err != nil {
		h.internal(w, r, "consumir desafio", err)
		return
	}
	if !consumido {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	// O carimbo é o da senha verificada no login, antes do desafio: se ela
	// mudou desde então (set-password, troca de senha), Issue recusa, e o
	// desafio obtido com a senha antiga morre com ela.
	h.issue(w, r, userID, desafio.Stamp)
}

type passwordRequest struct {
	Password string `json:"password"`
}

// senhaConfere escreve o 401 e devolve false quando a senha não bate. O erro
// é o mesmo CodeInvalidCredentials do login, pelo motivo do R34: um código
// distinto aqui contaria ao atacante que a sessão que ele roubou é válida e
// que só falta a senha.
//
// É a verificação do login (auth.CheckPassword, D4 do Plano 5): conta
// bloqueada é recusada e senha errada conta no bloqueio progressivo. Antes,
// esta função só comparava a senha, e um access token roubado testava senhas
// aqui sem nunca travar a conta.
func (h *AuthHandlers) senhaConfere(w http.ResponseWriter, r *http.Request, user auth.User, senha string) bool {
	ok, _, err := auth.CheckPassword(r.Context(), h.users, user, senha, h.clk.Now())
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
//
// Prova errada ou ausente é 401 invalid_credentials, e NUNCA unauthorized
// (D24 do Plano 5, apontado pelo Plano 6): unauthorized quer dizer "token
// recusado", e o painel responde a ele renovando a sessão; um código digitado
// errado viraria renovação, e depois logout. O código errado conta no mesmo
// bloqueio do login, como no /totp/verify (R40).
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
	if !decodeJSON(w, r, &req) {
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
	now := h.clk.Now()
	if prova == "" {
		// Sem a segunda prova o pedido não chega ao serviço: a senha sozinha
		// nunca desliga o fator. Ausente conta como errada.
		if err := h.users.RecordFailure(r.Context(), userID, now); err != nil {
			slog.Error("registrar falha de segundo fator", "erro", err)
		}
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}

	err = h.totp.Disable(r.Context(), userID, prova, now)
	if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
		if err := h.users.RecordFailure(r.Context(), userID, now); err != nil {
			slog.Error("registrar falha de segundo fator", "erro", err)
		}
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "desativar TOTP", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// TOTPRotate inicia a troca de autenticador (Plano 9, §3.2): grava o segredo
// do app novo como PENDENTE e devolve a otpauth_uri dele, no formato do
// enroll. O app antigo continua valendo até o rotate/confirm.
//
// As provas são as do TOTPDisable, pelos mesmos motivos: a senha (a
// verificação do login, que conta no bloqueio) E a posse do fator atual, por
// código do app ou de recuperação, em code ou recovery_code. Só a sessão
// entregaria o segundo fator a quem a roubou (R38).
//
// 2FA desligado é 403 forbidden, o espelho do enroll com o 2FA ligado; prova
// errada ou ausente é 401 invalid_credentials, nunca unauthorized (D24 do
// Plano 5), e conta no bloqueio (R40).
func (h *AuthHandlers) TOTPRotate(w http.ResponseWriter, r *http.Request) {
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
	if !decodeJSON(w, r, &req) {
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
	now := h.clk.Now()
	uri, err := h.totp.StartRotation(r.Context(), userID, user.Email, prova, now)
	if errors.Is(err, auth.ErrTOTPNotEnrolled) {
		apierr.WriteError(w, r, apierr.CodeForbidden)
		return
	}
	if errors.Is(err, auth.ErrTOTPInvalid) {
		// Ausente conta como errada, como no disable: a senha sozinha nunca
		// troca o fator.
		if err := h.users.RecordFailure(r.Context(), userID, now); err != nil {
			slog.Error("registrar falha de segundo fator", "erro", err)
		}
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "iniciar troca de TOTP", err)
		return
	}
	apierr.WriteJSON(w, http.StatusOK, map[string]string{"otpauth_uri": uri})
}

// TOTPRotateConfirm conclui a troca com um código do app NOVO e devolve os 10
// códigos de recuperação novos, no formato do confirm. As outras sessões caem;
// a do token fica.
//
// Sem troca em andamento é 403 forbidden (concluída ou cancelada em outra
// aba). Código errado é 401 invalid_credentials, conta no bloqueio, e o
// pendente fica. Conta bloqueada é o mesmo 401 antes do código, como no
// /totp/verify (R40): senão o confirm seria um lugar para chutar seis dígitos
// que o bloqueio não alcança.
func (h *AuthHandlers) TOTPRotateConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !ok || !okF {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	var req codeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	now := h.clk.Now()
	user, err := h.users.ByID(r.Context(), userID)
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}
	if user.IsLocked(now) {
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}

	codes, err := h.totp.ConfirmRotation(r.Context(), userID, familyID, req.Code, now)
	switch {
	case errors.Is(err, auth.ErrTOTPNoRotation):
		apierr.WriteError(w, r, apierr.CodeForbidden)
	case errors.Is(err, auth.ErrTOTPInvalid):
		if err := h.users.RecordFailure(r.Context(), userID, now); err != nil {
			slog.Error("registrar falha de segundo fator", "erro", err)
		}
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
	case errors.Is(err, auth.ErrInvalidSession):
		// A família caiu entre a guarda da rota e a trava: o mesmo 401
		// unauthorized da guarda.
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	case err != nil:
		h.internal(w, r, "confirmar troca de TOTP", err)
	default:
		// Única vez em que os códigos novos aparecem em claro.
		apierr.WriteJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
	}
}
