package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
)

const refreshCookieName = "vigilume_refresh"

// deviceCookieName é o cookie de dispositivo (A3 do Plano 10): ver
// auth.Devices. Mesmo Path do refresh, e por isso vai no login e no
// /totp/verify, os dois lugares que o leem.
const deviceCookieName = "vigilume_dispositivo"

type AuthHandlers struct {
	users        auth.UserStore
	sessions     *auth.Manager
	totp         *auth.TOTP
	clk          clock.Clock
	jwtSecret    []byte
	secureCookie bool
	// proxies decide se os cabeçalhos de proxy desta requisição valem. Ver
	// middleware.ClientIP: sem a lista, acreditar no cabeçalho seria pior que
	// ignorá-lo.
	proxies middleware.Proxies
	// provas conta as provas erradas das rotas autenticadas por família de
	// sessão (A3 do Plano 10); a mesma instância do AccountHandlers.
	provas *provasDaFamilia
	// dispositivos emite e confere o cookie de dispositivo, e conta as falhas
	// de login de cada um (A3 do Plano 10).
	dispositivos *auth.Devices
}

func NewAuthHandlers(users auth.UserStore, sessions *auth.Manager, totp *auth.TOTP, clk clock.Clock, jwtSecret []byte, secureCookie bool, proxies middleware.Proxies, provas *provasDaFamilia, dispositivos *auth.Devices) *AuthHandlers {
	return &AuthHandlers{users: users, sessions: sessions, totp: totp, clk: clk, jwtSecret: jwtSecret, secureCookie: secureCookie, proxies: proxies, provas: provas, dispositivos: dispositivos}
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
//
// A tentativa é reservada antes do Argon2id (A2 do Plano 10), no contador
// que h.tentativas escolhe: o do cookie de dispositivo, se ele vale para esta
// conta, ou o bloqueio da conta (A3).
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	now := h.clk.Now()

	user, err := h.users.ByEmail(r.Context(), auth.NormalizeEmail(req.Email))
	if errors.Is(err, auth.ErrNotFound) {
		if err := auth.VerifyDummy(r.Context()); err != nil {
			h.internal(w, r, "verificar senha", err)
			return
		}
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "buscar usuário", err)
		return
	}

	// CheckPassword recusa o contador bloqueado sem olhar a senha. O bloqueio
	// não se anuncia (R34, spec §5.2): a resposta é o mesmo 401 genérico de
	// senha errada e de e-mail inexistente.
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
	tentativas := h.tentativas(r, user, now)
	ok, needsRehash, err := auth.CheckPassword(r.Context(), tentativas, user, req.Password)
	if err != nil {
		h.internal(w, r, "verificar senha", err)
		return
	}
	if !ok {
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}

	// verificado é o hash que conferiu com a senha: é ele que a sessão (ou o
	// desafio do segundo fator) leva como prova, e Issue recusa se ele já não
	// for o hash do usuário (Task 11 do Plano 5).
	verificado := user.PasswordHash
	if needsRehash {
		// O rehash é um compare-and-swap: se um set-password comitou depois da
		// verificação, ele não grava, a prova continua sendo o hash antigo, e
		// Issue recusa a sessão. Sem o CAS, o rehash desfazia a redefinição.
		if novo, err := auth.HashPassword(r.Context(), req.Password); err == nil {
			trocou, err := h.users.RehashPassword(r.Context(), user.ID, user.PasswordHash, novo)
			if err != nil {
				slog.Error("regravar hash de senha", "erro", err)
			}
			if trocou {
				verificado = novo
			}
		}
	}

	// Com segundo fator ativo, a senha correta sozinha não abre sessão
	// nenhuma: o que sai daqui é um desafio de vida curta, com audience
	// própria, que ParseAccessToken recusa como credencial.
	enabled, err := h.totp.IsEnabled(r.Context(), user.ID)
	if err != nil {
		devolverTentativa(r, tentativas)
		h.internal(w, r, "consultar segundo fator", err)
		return
	}
	if enabled {
		// A senha certa não termina o login: a reserva dela volta (Refund),
		// sem zerar o contador. Zerar aqui daria ao atacante que já tem a
		// senha um botão de destravar entre um chute de seis dígitos e o
		// próximo, e o bloqueio do R40 nunca chegaria ao limiar.
		devolverTentativa(r, tentativas)
		challenge, err := auth.NewChallengeToken(h.jwtSecret, user.ID, auth.PasswordStamp(verificado), now)
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

	// O contador só zera quando o login de fato termina. Com segundo fator
	// ativo ele termina em /totp/verify, e é lá que o reset acontece.
	if err := tentativas.Reset(r.Context()); err != nil {
		slog.Error("zerar falhas de login", "erro", err)
	}

	// O cookie de dispositivo sai com o carimbo do hash verificado (o do
	// rehash, se houve): é ele o hash da conta daqui em diante.
	h.issue(w, r, user.ID, auth.PasswordStamp(verificado), nil, auth.NewDeviceStamp(verificado, user.TOTPConfirmedAt))
}

// tentativas escolhe o contador do login (A3 do Plano 10): o do cookie de
// dispositivo, se ele vale para esta conta e não está sendo ignorado (errou o
// limite há menos de 15 min); senão, o bloqueio da conta, como antes. O
// limitador por IP vale para os dois.
func (h *AuthHandlers) tentativas(r *http.Request, user auth.User, now time.Time) auth.Attempts {
	if ck, err := r.Cookie(deviceCookieName); err == nil {
		// O carimbo das credenciais de agora (I1 da revisão do Plano 10): o
		// cookie cunhado antes de uma troca de senha, set-password ou
		// reset-2fa não vale mais.
		if id, ok := h.dispositivos.Check(ck.Value, user.ID, user.DeviceStamp()); ok && !h.dispositivos.Ignored(id) {
			return h.dispositivos.Attempts(id)
		}
	}
	return auth.AccountAttempts(h.users, user.ID, now)
}

// devolverTentativa devolve a reserva de uma prova certa que não terminou o
// login (ver auth.Attempts). Falhar ao devolver não muda a resposta: a prova
// estava certa, e o pior caso é uma tentativa a mais contada.
func devolverTentativa(r *http.Request, t auth.Attempts) {
	if err := t.Refund(r.Context()); err != nil {
		slog.Error("devolver tentativa de login", "erro", err)
	}
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

	h.grantSession(w, r, token, s, nil)
}

// Logout encerra a família do refresh apresentado e apaga o cookie.
//
// Se a revogação falha no banco, a resposta é 500 e o cookie FICA (A9 do
// Plano 10): antes, o erro ia só para o log, o navegador recebia 204 e perdia
// o cookie, e a família continuava viva por até 30 dias no servidor — com
// quem tivesse copiado o refresh ainda dentro, e o dono achando que tinha
// saído. Com o cookie mantido, o painel pode avisar e tentar de novo.
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(refreshCookieName); err == nil {
		if err := h.sessions.Revoke(r.Context(), ck.Value); err != nil {
			h.internal(w, r, "revogar sessão", err)
			return
		}
	}
	h.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// issue abre a sessão de um login cuja senha conferiu; stamp é o carimbo do
// hash verificado. Se a senha mudou no meio (set-password, troca de senha), a
// resposta é o mesmo 401 da senha errada: a senha que o cliente mandou já não
// é a da conta (R34).
//
// Com o 2FA (prova não nil), a sessão também só nasce se o segredo que aceitou
// o código ainda for o ativo (A4 do Plano 10); se uma troca de autenticador ou
// um disable comitou no meio, o mesmo 401 invalid_credentials.
//
// dispositivo é o carimbo das credenciais para o cookie de dispositivo que o
// login completo recebe (ver auth.DeviceStamp).
func (h *AuthHandlers) issue(w http.ResponseWriter, r *http.Request, userID uuid.UUID, stamp string, prova *auth.SecondFactorProof, dispositivo auth.DeviceStamp) {
	var token string
	var s auth.Session
	var err error
	if prova != nil {
		token, s, err = h.sessions.IssueAfterSecondFactor(r.Context(), userID, stamp, *prova, h.meta(r))
	} else {
		token, s, err = h.sessions.Issue(r.Context(), userID, stamp, h.meta(r))
	}
	if errors.Is(err, auth.ErrStalePassword) || errors.Is(err, auth.ErrStaleSecondFactor) {
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
		return
	}
	if err != nil {
		h.internal(w, r, "emitir sessão", err)
		return
	}
	h.grantSession(w, r, token, s, &dispositivo)
}

// grantSession entrega a sessão ao cliente, e a ORDEM aqui é o ponto (item
// 6): o access token é assinado ANTES de o cookie de refresh ir para a
// resposta. Antes era o contrário, e como apierr.WriteJSON não limpa os
// cabeçalhos já escritos, uma falha ao assinar saía como 500 COM o
// Set-Cookie de uma sessão válida — o navegador ficava com um refresh vivo
// enquanto o usuário via "o login falhou".
//
// O que a reordenação NÃO cobre: a linha em sessions já foi gravada por
// Issue/Rotate antes desta função ser chamada, então um 500 aqui ainda deixa
// a sessão no banco. O que ela tira é o cookie do lado do cliente, que é a
// metade explorável: sessão órfã no banco expira sozinha e ninguém a
// apresenta; cookie vivo no navegador é credencial em uso.
//
// O access token leva a família da sessão (claim sid): é a mesma família do
// refresh que vai no cookie.
//
// dispositivo não nil diz que a sessão nasce de um login completo (senha e,
// se ligado, o 2FA), e não de uma renovação: só aí sai um cookie de
// dispositivo novo (A3 do Plano 10), com esse carimbo das credenciais, pela
// mesma ordem — depois de o access token ser assinado.
func (h *AuthHandlers) grantSession(w http.ResponseWriter, r *http.Request, refresh string, s auth.Session, dispositivo *auth.DeviceStamp) {
	access, err := auth.NewAccessToken(h.jwtSecret, s.UserID, s.FamilyID, h.clk.Now())
	if err != nil {
		h.internal(w, r, "assinar access token", err)
		return
	}
	h.setCookie(w, refresh, s.ExpiresAt)
	if dispositivo != nil {
		h.setDeviceCookie(w, s.UserID, *dispositivo)
	}
	apierr.WriteJSON(w, http.StatusOK, tokenResponse{
		AccessToken: access,
		ExpiresIn:   int(auth.AccessTTL.Seconds()),
	})
}

func (h *AuthHandlers) setCookie(w http.ResponseWriter, token string, expires time.Time) {
	// #nosec G124 -- falso positivo: a regra exige o literal true em Secure, mas aqui ele é derivado de PUBLIC_BASE_URL (routes.go) para permitir desenvolvimento local sem TLS; em produção a URL é https e o cookie sai Secure, e HttpOnly/SameSite=Strict já estão fixos.
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

// setDeviceCookie põe o cookie de dispositivo (A3 do Plano 10). Os atributos
// são os do refresh (HttpOnly, SameSite=Strict, Path /api/v1/auth, Secure com
// https), e a validade é auth.DeviceTTL. Uma falha ao sortear o identificador
// não derruba o login: o próximo login completo tenta de novo.
func (h *AuthHandlers) setDeviceCookie(w http.ResponseWriter, userID uuid.UUID, carimbo auth.DeviceStamp) {
	valor, _, err := h.dispositivos.Issue(userID, carimbo)
	if err != nil {
		slog.Error("emitir cookie de dispositivo", "erro", err)
		return
	}
	// #nosec G124 -- falso positivo: mesmo caso do setCookie; Secure vem de PUBLIC_BASE_URL (routes.go), com HttpOnly e SameSite=Strict fixos.
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookieName,
		Value:    valor,
		Path:     "/api/v1/auth",
		MaxAge:   int(auth.DeviceTTL / time.Second),
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandlers) clearCookie(w http.ResponseWriter) {
	apagarCookieDeRefresh(w, h.secureCookie)
}

// apagarCookieDeRefresh é o logout do lado do navegador. Mora fora do
// AuthHandlers porque DELETE /sessions/{id} da própria sessão também apaga o
// cookie, e os atributos têm de ser os mesmos do setCookie.
func apagarCookieDeRefresh(w http.ResponseWriter, secure bool) {
	// #nosec G124 -- falso positivo: mesmo caso do setCookie; Secure vem de PUBLIC_BASE_URL (routes.go) para não quebrar o desenvolvimento local sem TLS, com HttpOnly e SameSite=Strict fixos.
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// meta usa o MESMO endereço que o limitador, e não o RemoteAddr cru. A §4.2
// criou ip_hash para correlacionar tentativas da mesma origem; atrás de um
// proxy, o RemoteAddr é o proxy e a coluna passa a correlacionar nada. Ao
// contrário do rate limit, esse dano não tem volta: linha já gravada com o
// hash errado não se recupera.
func (h *AuthHandlers) meta(r *http.Request) auth.Meta {
	return auth.Meta{IP: middleware.ClientIP(r, h.proxies), UserAgent: r.UserAgent()}
}

// internal registra o erro real e devolve ao cliente apenas o envelope
// genérico com o trace_id (spec §9.1). O teto do Argon2id cheio não é erro
// interno: é o 429 de argon2Ocupado.
func (h *AuthHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, auth.ErrArgon2Busy) {
		argon2Ocupado(w, r, op)
		return
	}
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
}

// argon2Ocupado é a resposta de quando o teto de Argon2id simultâneos ficou
// cheio pelo prazo inteiro (A1 do Plano 10): 429 rate_limited, com
// Retry-After igual ao prazo. A senha não foi verificada, e nada contou no
// bloqueio. Vale para a conta existente e para a inexistente (o VerifyDummy
// passa pelo mesmo semáforo), então não enumera contas.
func argon2Ocupado(w http.ResponseWriter, r *http.Request, op string) {
	slog.Warn("teto de Argon2id simultâneos atingido", "op", op, "trace_id", observability.TraceID(r.Context()))
	w.Header().Set("Retry-After", strconv.Itoa(int(auth.Argon2Wait/time.Second)))
	apierr.WriteError(w, r, apierr.CodeRateLimited)
}

// decodeJSON lê o corpo JSON e já distingue os dois modos de falha que o
// cliente precisa separar: "seus dados são inválidos" (400) e "seu corpo é
// grande demais" (413). O segundo chega tipado como *http.MaxBytesError,
// posto ali pelo middleware.BodyLimit; tratá-lo como validação, como antes,
// mandava 400 para quem não tinha erro de validação nenhum.
//
// Devolve false quando já escreveu o erro — o chamador só precisa retornar.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONWith(w, r, dst, false)
}

// decodeJSONStrict recusa campo desconhecido. Nas rotas de recurso, um campo
// com o nome errado ("intervalsec") seria ignorado em silêncio, e o cliente
// acharia que salvou.
func decodeJSONStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONWith(w, r, dst, true)
}

func decodeJSONWith(w http.ResponseWriter, r *http.Request, dst any, strict bool) bool {
	dec := json.NewDecoder(r.Body)
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			apierr.WriteError(w, r, apierr.CodePayloadTooLarge)
			return false
		}
		// O erro de sintaxe chega ANTES do limite quando o corpo gigante nem
		// é JSON: o decoder para no primeiro byte inválido, muito longe do
		// 1 MiB, e nunca vê o MaxBytesError. Drenar o resto (que o próprio
		// MaxBytesReader mantém limitado) revela se o corpo também estourava
		// o limite; quando estoura, o tamanho é a causa mais útil de relatar
		// — mandar 400 faria o cliente procurar um defeito nos dados que o
		// servidor sequer chegou a ler.
		if _, drenar := io.Copy(io.Discard, r.Body); errors.As(drenar, &tooLarge) {
			apierr.WriteError(w, r, apierr.CodePayloadTooLarge)
			return false
		}
		apierr.WriteError(w, r, apierr.CodeValidation)
		return false
	}
	return true
}
