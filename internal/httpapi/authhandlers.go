package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

const refreshCookieName = "vigilume_refresh"

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
	if !decodeJSON(w, r, &req) {
		return
	}
	now := h.clk.Now()

	user, err := h.users.ByEmail(r.Context(), normalizarEmail(req.Email))
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

	h.grantSession(w, r, token, s)
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
	h.grantSession(w, r, token, s)
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
func (h *AuthHandlers) grantSession(w http.ResponseWriter, r *http.Request, refresh string, s auth.Session) {
	access, err := auth.NewAccessToken(h.jwtSecret, s.UserID, s.FamilyID, h.clk.Now())
	if err != nil {
		h.internal(w, r, "assinar access token", err)
		return
	}
	h.setCookie(w, refresh, s.ExpiresAt)
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

func (h *AuthHandlers) clearCookie(w http.ResponseWriter) {
	// #nosec G124 -- falso positivo: mesmo caso do setCookie; Secure vem de PUBLIC_BASE_URL (routes.go) para não quebrar o desenvolvimento local sem TLS, com HttpOnly e SameSite=Strict fixos.
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

// normalizarEmail tira apenas o espaço em volta. O caso real é o
// autocompletar do celular, que costuma acrescentar um espaço à direita:
// sem isto, " pessoa@exemplo.com " devolve 401 para a credencial certa.
//
// NÃO aplica ToLower de propósito. A coluna é citext, então a caixa já é
// comparada sem distinção no banco; baixar a caixa aqui seria redundante e,
// por rodar só no login, mascararia a ausência do citext caso uma migration
// futura trocasse o tipo da coluna — o login continuaria funcionando e o
// cadastro passaria a gravar caixa mista sem que nenhum teste reclamasse.
//
// PENDENTE: esta função precisa migrar para o pacote auth assim que a rota
// de cadastro existir, e o cadastro tem de usar EXATAMENTE a mesma
// normalização. Duas normalizações diferentes entre cadastro e login criam
// uma discrepância pior que o problema original: o usuário cadastra um
// e-mail que nunca mais consegue usar para entrar.
func normalizarEmail(email string) string {
	return strings.TrimSpace(email)
}
