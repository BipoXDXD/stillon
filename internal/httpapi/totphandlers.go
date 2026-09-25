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
//
// A senha errada conta no contador da família (A3 do Plano 10), como em toda
// rota autenticada: ver provasDaFamilia.
func (h *AuthHandlers) TOTPEnroll(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !ok || !okF {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	var req passwordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	res := h.provas.reservar(w, r, userID, familyID)
	if res == nil {
		return
	}
	defer res.Release()

	user, err := h.users.ByID(r.Context(), userID)
	if err != nil {
		h.usuarioOuInterno(w, r, "buscar usuário", err)
		return
	}

	if !h.senhaConfere(w, r, res, user, req.Password) {
		return
	}

	uri, err := h.totp.Enroll(r.Context(), userID, user.Email)
	if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
		// R38: recadastrar não pode ser a porta dos fundos para desligar o
		// segundo fator de quem já o tem ativo.
		//
		// E a senha certa aqui NÃO zera a família (C1 da revisão do Plano
		// 10): a reserva volta pelo Release do defer. Zerada, quem tem a
		// sessão e a senha, mas não o fator, chutava quatro códigos no
		// disable ou no rotate, mandava um enroll, e chutava mais quatro,
		// sem fim. Com o 2FA ligado, só a prova do fator zera a família.
		apierr.WriteError(w, r, apierr.CodeForbidden)
		return
	}
	if err != nil {
		h.usuarioOuInterno(w, r, "cadastrar TOTP", err)
		return
	}
	// Só depois de o cadastro dar certo: aí o 2FA estava desligado, e a
	// senha era toda a credencial que havia a provar.
	res.Succeed()
	// A URI otpauth:// vai para o cliente, que renderiza o QR code. Gerar a
	// imagem no servidor só acrescentaria payload.
	apierr.WriteJSON(w, http.StatusOK, map[string]string{"otpauth_uri": uri})
}

// TOTPConfirm liga o 2FA com um código do app recém-cadastrado. O código
// errado é 400 validation_failed, como sempre, e conta no contador da família
// (A3 do Plano 10): antes não contava em lugar nenhum, e um ladrão de sessão
// chutava seis dígitos aqui contra o cadastro abandonado do dono, limitado só
// por IP. A quinta derruba a família (401 unauthorized).
func (h *AuthHandlers) TOTPConfirm(w http.ResponseWriter, r *http.Request) {
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
	res := h.provas.reservar(w, r, userID, familyID)
	if res == nil {
		return
	}
	defer res.Release()

	codes, err := h.totp.Confirm(r.Context(), userID, req.Code, h.clk.Now())
	if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
		// Mesmo 403 do enroll: reconfirmar rotacionaria os códigos de
		// recuperação e apagaria a data de ativação original. O Confirm
		// recusa antes de olhar o código (C1 da revisão do Plano 10), então
		// o 403 é o mesmo para o código certo e o errado, e não é oráculo.
		// Nada foi provado: a reserva volta, e a família não zera.
		apierr.WriteError(w, r, apierr.CodeForbidden)
		return
	}
	if errors.Is(err, auth.ErrTOTPInvalid) {
		h.provas.errou(w, r, res, userID, apierr.CodeValidation)
		return
	}
	if errors.Is(err, auth.ErrTOTPNotEnrolled) {
		// Sem cadastro não há código a errar: a reserva volta.
		apierr.WriteError(w, r, apierr.CodeValidation)
		return
	}
	if err != nil {
		h.usuarioOuInterno(w, r, "confirmar TOTP", err)
		return
	}
	res.Succeed()

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
// Ordem: desafio → usuário → pré-checagem da marca → reserva da tentativa →
// código → Reset → ConsumeChallenge → sessão. A pré-checagem recusa antes do
// código o desafio que já não pode abrir sessão, para não gastar um código de
// recuperação com ele (revisão final do Plano 8).
//
// A tentativa é reservada ANTES do código (A2 do Plano 10): antes, o bloqueio
// era lido antes e a falha gravada depois, e quem tinha a senha disparava
// centenas de códigos simultâneos de várias origens, todos passando pela
// leitura antes de a quinta falha ser gravada. O contador é o do login
// (h.tentativas): o cookie de dispositivo, se vale para a conta, ou o
// bloqueio da conta (A3).
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

	user, err := h.users.ByID(r.Context(), userID)
	if errors.Is(err, auth.ErrNotFound) {
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
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

	// O segundo fator entra no MESMO contador do login (R40). Sem isto, quem
	// já tem a senha chuta seis dígitos indefinidamente: o limite de
	// /totp/verify é por IP, e um atacante com várias origens passa por cima
	// dele sem que a conta jamais tranque. Bloqueado é o mesmo 401 genérico
	// do login (R34), sem olhar o código.
	tentativas := h.tentativas(r, user, now)
	reservou, err := tentativas.Reserve(r.Context())
	if err != nil {
		h.internal(w, r, "reservar tentativa de segundo fator", err)
		return
	}
	if !reservou {
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}

	prova, err := h.totp.VerifyWithProof(r.Context(), userID, req.Code, now)
	if err != nil {
		// O mesmo 401 genérico do login errado (R34): um código distinto para
		// "segundo fator falhou" contaria ao atacante que a senha estava
		// certa, e a senha é justamente o que ele está testando. A reserva
		// fica contada.
		if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
			apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
			return
		}
		devolverTentativa(r, tentativas)
		h.internal(w, r, "verificar TOTP", err)
		return
	}

	if err := tentativas.Reset(r.Context()); err != nil {
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
	// desafio obtido com a senha antiga morre com ela. A prova do 2FA é
	// reconferida do mesmo jeito (A4 do Plano 10).
	h.issue(w, r, userID, desafio.Stamp, &prova, user.DeviceStamp())
}

type passwordRequest struct {
	Password string `json:"password"`
}

// usuarioOuInterno é o erro das rotas do 2FA: o usuário do token que sumiu
// no meio da requisição (auth.ErrNotFound) é 401 unauthorized, como no /me e
// na troca de senha (A10 do Plano 10) — o painel volta ao login em vez de
// mostrar um erro interno. O resto é h.internal.
func (h *AuthHandlers) usuarioOuInterno(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, auth.ErrNotFound) {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	h.internal(w, r, op, err)
}

// senhaConfere escreve o erro e devolve false quando a senha não bate. O erro
// é o mesmo CodeInvalidCredentials do login, pelo motivo do R34: um código
// distinto aqui contaria ao atacante que a sessão que ele roubou é válida e
// que só falta a senha.
//
// A senha errada conta no contador da família da reserva (A3 do Plano 10), e
// a quinta derruba a família. O bloqueio da CONTA não entra: nem recusa a
// conta bloqueada (o dono logado não fica sem trocar a própria credencial
// porque um anônimo travou o login), nem conta a falha (quem tem a sessão e
// chuta a senha é expulso da sessão, e não tranca o dono).
func (h *AuthHandlers) senhaConfere(w http.ResponseWriter, r *http.Request, res *auth.ProofReservation, user auth.User, senha string) bool {
	ok, _, err := auth.VerifyPassword(r.Context(), user.PasswordHash, senha)
	if err != nil {
		h.internal(w, r, "verificar senha", err)
		return false
	}
	if !ok {
		h.provas.errou(w, r, res, user.ID, apierr.CodeInvalidCredentials)
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
// errado viraria renovação, e depois logout. A prova errada conta no contador
// da família (A3 do Plano 10); a quinta derruba a família, e aí sim a resposta
// é unauthorized.
func (h *AuthHandlers) TOTPDisable(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !ok || !okF {
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
	res := h.provas.reservar(w, r, userID, familyID)
	if res == nil {
		return
	}
	defer res.Release()

	user, err := h.users.ByID(r.Context(), userID)
	if err != nil {
		h.usuarioOuInterno(w, r, "buscar usuário", err)
		return
	}
	if !h.senhaConfere(w, r, res, user, req.Password) {
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
		h.provas.errou(w, r, res, userID, apierr.CodeInvalidCredentials)
		return
	}

	err = h.totp.Disable(r.Context(), userID, prova, now)
	if errors.Is(err, auth.ErrTOTPInvalid) || errors.Is(err, auth.ErrTOTPNotEnrolled) {
		h.provas.errou(w, r, res, userID, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.usuarioOuInterno(w, r, "desativar TOTP", err)
		return
	}
	res.Succeed()

	w.WriteHeader(http.StatusNoContent)
}

// TOTPRotate inicia a troca de autenticador (Plano 9, §3.2): grava o segredo
// do app novo como PENDENTE e devolve a otpauth_uri dele, no formato do
// enroll. O app antigo continua valendo até o rotate/confirm.
//
// As provas são as do TOTPDisable, pelos mesmos motivos: a senha E a posse do
// fator atual, por código do app ou de recuperação, em code ou recovery_code.
// Só a sessão entregaria o segundo fator a quem a roubou (R38).
//
// 2FA desligado é 403 forbidden, o espelho do enroll com o 2FA ligado; prova
// errada ou ausente é 401 invalid_credentials, nunca unauthorized (D24 do
// Plano 5), e conta no contador da família (A3 do Plano 10). O pendente fica
// amarrado à família do token; se ela caiu antes da gravação, 401
// unauthorized. O sucesso zera o bloqueio da conta (A6 do Plano 10).
func (h *AuthHandlers) TOTPRotate(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !ok || !okF {
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
	res := h.provas.reservar(w, r, userID, familyID)
	if res == nil {
		return
	}
	defer res.Release()

	user, err := h.users.ByID(r.Context(), userID)
	if err != nil {
		h.usuarioOuInterno(w, r, "buscar usuário", err)
		return
	}
	if !h.senhaConfere(w, r, res, user, req.Password) {
		return
	}

	prova := req.Code
	if prova == "" {
		prova = req.RecoveryCode
	}
	now := h.clk.Now()
	uri, rotacao, err := h.totp.StartRotation(r.Context(), userID, familyID, user.Email, prova, now)
	if errors.Is(err, auth.ErrTOTPNotEnrolled) {
		// Só a senha foi provada; nenhuma prova errou: a reserva volta.
		apierr.WriteError(w, r, apierr.CodeForbidden)
		return
	}
	if errors.Is(err, auth.ErrTOTPInvalid) {
		// Ausente conta como errada, como no disable: a senha sozinha nunca
		// troca o fator.
		h.provas.errou(w, r, res, userID, apierr.CodeInvalidCredentials)
		return
	}
	if errors.Is(err, auth.ErrInvalidSession) {
		// A família caiu entre a guarda da rota e a trava (uma troca de
		// senha, um DELETE /sessions): o mesmo 401 unauthorized da guarda, e
		// nenhum pendente gravado.
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	if err != nil {
		h.usuarioOuInterno(w, r, "iniciar troca de TOTP", err)
		return
	}
	res.Succeed()
	// rotation identifica ESTA troca (A7 do Plano 10): o rotate/confirm o
	// exige de volta.
	apierr.WriteJSON(w, http.StatusOK, map[string]string{"otpauth_uri": uri, "rotation": rotacao})
}

// TOTPRotateConfirm conclui a troca com um código do app NOVO e devolve os 10
// códigos de recuperação novos, no formato do confirm. As outras sessões caem;
// a do token fica. O corpo é {code, rotation}: rotation é o que o rotate
// devolveu (A7 do Plano 10); ausente ou de outra troca é o 403 de "sem troca",
// antes do código e sem contar prova errada.
//
// Sem troca em andamento é 403 forbidden (concluída ou cancelada em outra
// aba), e também quando a troca foi iniciada por outra família de sessão: o
// pendente é só da que o criou (revisão final do Plano 9). Código errado é
// 401 invalid_credentials, e o pendente fica.
//
// O código errado conta no contador da família (A3 do Plano 10), e a quinta
// prova errada derruba a família: é isso, e não mais o bloqueio da conta, que
// impede o confirm de virar um lugar para chutar seis dígitos. A conta
// bloqueada deixou de ser recusada aqui (era a D2 do Plano 9): o bloqueio é
// do login. O sucesso zera o bloqueio da conta (A6 do Plano 10).
func (h *AuthHandlers) TOTPRotateConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !ok || !okF {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	var req struct {
		Code     string `json:"code"`
		Rotation string `json:"rotation"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res := h.provas.reservar(w, r, userID, familyID)
	if res == nil {
		return
	}
	defer res.Release()

	codes, err := h.totp.ConfirmRotation(r.Context(), userID, familyID, req.Rotation, req.Code, h.clk.Now())
	switch {
	case errors.Is(err, auth.ErrTOTPNoRotation):
		apierr.WriteError(w, r, apierr.CodeForbidden)
	case errors.Is(err, auth.ErrTOTPInvalid):
		h.provas.errou(w, r, res, userID, apierr.CodeInvalidCredentials)
	case errors.Is(err, auth.ErrInvalidSession):
		// A família caiu entre a guarda da rota e a trava: o mesmo 401
		// unauthorized da guarda.
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	case err != nil:
		h.usuarioOuInterno(w, r, "confirmar troca de TOTP", err)
	default:
		res.Succeed()
		// Única vez em que os códigos novos aparecem em claro.
		apierr.WriteJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
	}
}
