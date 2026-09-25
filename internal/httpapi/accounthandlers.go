package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/account"
	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/httpapi/middleware"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

// AccountHandlers são as rotas da conta do dono (Plano 5): GET /me, POST
// /auth/password, GET /sessions e DELETE /sessions/{id}.
type AccountHandlers struct {
	svc     *account.Service
	proxies middleware.Proxies
	// secureCookie: o mesmo critério do AuthHandlers, para apagar o cookie de
	// refresh quando a sessão encerrada é a atual.
	secureCookie bool
	// provas: o contador de provas erradas por família (A3 do Plano 10), o
	// mesmo das rotas do 2FA.
	provas *provasDaFamilia
}

func NewAccountHandlers(svc *account.Service, proxies middleware.Proxies, secureCookie bool, provas *provasDaFamilia) *AccountHandlers {
	return &AccountHandlers{svc: svc, proxies: proxies, secureCookie: secureCookie, provas: provas}
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
// com o campo; senha atual ou segundo fator errado é o 401 genérico do login
// (R34); a sessão do token já encerrada é 401 unauthorized, que o painel
// trata renovando (e a renovação falha, levando ao login).
//
// A prova errada conta no contador da família (A3 do Plano 10), e a quinta
// derruba a família (401 unauthorized). O bloqueio da conta não entra aqui.
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
	res := h.provas.reservar(w, r, userID, familyID)
	if res == nil {
		return
	}
	defer res.Release()

	err := h.svc.ChangePassword(r.Context(), userID, familyID, account.ChangePasswordInput{
		Current: req.CurrentPassword, New: req.NewPassword, TOTPCode: req.TOTPCode,
	}, auth.Meta{IP: middleware.ClientIP(r, h.proxies), UserAgent: r.UserAgent()})
	var pe account.PolicyError
	switch {
	case errors.As(err, &pe):
		apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "new_password", Reason: pe.Reason}})
	case errors.Is(err, account.ErrSessionGone):
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	case errors.Is(err, account.ErrInvalidCredentials):
		h.provas.errou(w, r, res, userID, apierr.CodeInvalidCredentials)
	case err != nil:
		h.internal(w, r, "trocar senha", err)
	default:
		res.Succeed()
		w.WriteHeader(http.StatusNoContent)
	}
}

type sessionResponse struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
}

type sessionListResponse struct {
	Items []sessionResponse `json:"items"`
}

// sessaoViva é a guarda das rotas de sessão (D11 do Plano 5): o token tem de
// vir de uma família ainda viva. Sem ela, um access token de uma sessão já
// encerrada — o do invasor, depois de o dono encerrá-la — continuaria por até
// 15 minutos listando e derrubando as sessões do dono.
func (h *AccountHandlers) sessaoViva(w http.ResponseWriter, r *http.Request) (userID, familyID uuid.UUID, ok bool) {
	userID, okU := middleware.UserID(r.Context())
	familyID, okF := middleware.FamilyID(r.Context())
	if !okU || !okF {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return uuid.Nil, uuid.Nil, false
	}
	vivo, err := h.svc.FamilyIsLive(r.Context(), userID, familyID)
	if err != nil {
		h.internal(w, r, "conferir sessão", err)
		return uuid.Nil, uuid.Nil, false
	}
	if !vivo {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return uuid.Nil, uuid.Nil, false
	}
	return userID, familyID, true
}

// exigirSessaoViva é o sessaoViva como middleware, para as rotas do segundo
// fator, que moram no AuthHandlers: /auth/totp/enroll, /confirm e /disable
// (Task 11 do Plano 5). Sem ele, o access token de uma sessão que a troca de
// senha ou o set-password acabou de encerrar confirmava, nos 15 minutos que
// lhe restam, o 2FA que o invasor tinha cadastrado com o autenticador dele.
func (h *AccountHandlers) exigirSessaoViva(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := h.sessaoViva(w, r); !ok {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ListSessions é GET /sessions. O DTO é construído campo a campo: nada de IP,
// hash, vencimento nem id de linha.
func (h *AccountHandlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	userID, familyID, ok := h.sessaoViva(w, r)
	if !ok {
		return
	}
	sessoes, err := h.svc.ListSessions(r.Context(), userID, familyID)
	if err != nil {
		h.internal(w, r, "listar sessões", err)
		return
	}
	resp := sessionListResponse{Items: make([]sessionResponse, 0, len(sessoes))}
	for _, s := range sessoes {
		resp.Items = append(resp.Items, sessionResponse{
			ID: s.ID.String(), CreatedAt: s.CreatedAt, LastUsedAt: s.LastUsedAt,
			UserAgent: s.UserAgent, Current: s.Current,
		})
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

// RevokeSession é DELETE /sessions/{id}. Encerrar a própria sessão é
// permitido, e a resposta apaga o cookie de refresh: o navegador aceita apagar
// um cookie de outro Path (RFC 6265 §5.2.4 não valida o Path contra a
// requisição). O access token em mãos continua valendo até expirar nas rotas
// de recurso; descartá-lo é trabalho do painel.
func (h *AccountHandlers) RevokeSession(w http.ResponseWriter, r *http.Request) {
	userID, familyID, ok := h.sessaoViva(w, r)
	if !ok {
		return
	}
	alvo, ok := pathID(w, r)
	if !ok {
		return
	}
	err := h.svc.RevokeSession(r.Context(), userID, alvo,
		auth.Meta{IP: middleware.ClientIP(r, h.proxies), UserAgent: r.UserAgent()})
	if errors.Is(err, account.ErrSessionNotFound) {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return
	}
	if err != nil {
		h.internal(w, r, "revogar sessão", err)
		return
	}
	if alvo == familyID {
		apagarCookieDeRefresh(w, h.secureCookie)
	}
	w.WriteHeader(http.StatusNoContent)
}

// internal registra o erro real e devolve só o envelope genérico com o
// trace_id (spec §9.1). O teto do Argon2id cheio é o 429 de argon2Ocupado.
func (h *AccountHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, auth.ErrArgon2Busy) {
		argon2Ocupado(w, r, op)
		return
	}
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
}
