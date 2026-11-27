package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/turnstile"
)

// Cadastrador é o cadastro público (account.Cadastro).
type Cadastrador interface {
	Cadastrar(ctx context.Context, email, senha string) error
	ConfirmarEmail(ctx context.Context, token, senha string, ipHash []byte) error
	ReenviarConfirmacao(ctx context.Context, userID uuid.UUID) error
}

// Recuperador é o "esqueci a senha" (account.Recuperacao).
type Recuperador interface {
	PedirRedefinicao(ctx context.Context, email string) error
	Redefinir(ctx context.Context, token, novaSenha string) error
}

// VerificadorTurnstile confere o desafio do widget (turnstile.Verificador).
// turnstile.ErrRecusado é desafio inválido; qualquer outro erro é o
// siteverify fora do ar.
type VerificadorTurnstile interface {
	Verificar(ctx context.Context, token, ip, acao string) error
}

// ConfigCadastro diz se o cadastro público está ligado e qual site key o
// widget usa. Desligado, o signup e o forgot respondem 404 e a configuração
// pública sai sem a chave.
type ConfigCadastro struct {
	Habilitado bool
	SiteKey    string
}

// As ações que o painel declara no widget: um token emitido para o signup não
// vale no forgot, e vice-versa.
const (
	acaoSignup = "signup"
	acaoForgot = "forgot"
)

// tamanhoMaximoDoTokenDeConta: o token de conta tem 256 bits em base64url (43
// caracteres). Bem mais que isso é lixo, e vira token_invalid sem consultar
// o banco.
const tamanhoMaximoDoTokenDeConta = 128

// CadastroHandlers são as rotas públicas do cadastro e da recuperação de
// senha, mais o reenvio da confirmação (com sessão).
type CadastroHandlers struct {
	cadastro    Cadastrador
	recuperacao Recuperador
	turnstile   VerificadorTurnstile
	config      ConfigCadastro
	proxies     middleware.Proxies
	// ipKey é a chave do HMAC do IP, a mesma do login (auth.Manager.IPHash):
	// a auditoria da trava vinda da confirmação grava o ip_hash como a do
	// login. clk dá o agora do Retry-After da trava.
	ipKey []byte
	clk   clock.Clock
}

func NewCadastroHandlers(c Cadastrador, r Recuperador, t VerificadorTurnstile, cfg ConfigCadastro, proxies middleware.Proxies, ipKey []byte, clk clock.Clock) *CadastroHandlers {
	return &CadastroHandlers{cadastro: c, recuperacao: r, turnstile: t, config: cfg, proxies: proxies, ipKey: ipKey, clk: clk}
}

type configCadastroResponse struct {
	Enabled          bool   `json:"enabled"`
	TurnstileSiteKey string `json:"turnstile_site_key"`
}

// Config é GET /auth/signup-config: o painel decide se mostra o formulário e
// com que site key monta o widget. Desligado, a chave sai vazia.
func (h *CadastroHandlers) Config(w http.ResponseWriter, _ *http.Request) {
	resp := configCadastroResponse{Enabled: h.config.Habilitado}
	if h.config.Habilitado {
		resp.TurnstileSiteKey = h.config.SiteKey
	}
	apierr.WriteJSON(w, http.StatusOK, resp)
}

type signupRequest struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	TurnstileToken string `json:"turnstile_token"`
}

// Signup é POST /auth/signup: 202 sem corpo, com conta nova ou já existente.
//
// A ordem é a do plano: desligado (404), corpo, formato (só olha a entrada,
// então não enumera, e não gasta o token de uso único do Turnstile num
// formulário errado), Turnstile (recusado é 400 antes de qualquer consulta;
// fora do ar é 503, falha fechada) e só então o serviço, que responde igual
// nos dois ramos.
func (h *CadastroHandlers) Signup(w http.ResponseWriter, r *http.Request) {
	if !h.config.Habilitado {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return
	}
	var req signupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	var campos []apierr.FieldError
	if _, err := account.ValidateEmail(req.Email); err != nil {
		campos = append(campos, apierr.FieldError{Field: "email", Reason: "invalid"})
	}
	if motivo := auth.ValidateNewPassword(req.Password); motivo != "" {
		campos = append(campos, apierr.FieldError{Field: "password", Reason: motivo})
	}
	if len(campos) > 0 {
		apierr.WriteValidation(w, r, campos)
		return
	}
	if !h.desafioAceito(w, r, req.TurnstileToken, acaoSignup) {
		return
	}
	if err := h.cadastro.Cadastrar(r.Context(), req.Email, req.Password); err != nil {
		h.internal(w, r, "cadastrar", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

type confirmarEmailRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// ConfirmarEmail é POST /auth/email/confirm, com o token do link e a senha
// escolhida no cadastro:
//
//   - 204: confirmado;
//   - 400 token_invalid: token inexistente, usado ou vencido, sem distinção, e
//     antes de olhar a senha;
//   - 401 invalid_credentials: token válido e senha errada ou vazia. O link
//     NÃO é gasto: dá para tentar de novo;
//   - 429 rate_limited: o balde por IP, o teto do Argon2id ou a conta
//     travada pelo bloqueio do login (cinco senhas erradas, no login ou aqui).
//     A trava se anuncia aqui, ao contrário do login: quem chega à senha já
//     tem o token, que prova que a conta existe. O Retry-After da trava é o
//     que falta dela, arredondado para cima.
func (h *CadastroHandlers) ConfirmarEmail(w http.ResponseWriter, r *http.Request) {
	var req confirmarEmailRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !tokenDeContaPlausivel(req.Token) {
		apierr.WriteError(w, r, apierr.CodeTokenInvalid)
		return
	}
	ipHash := crypto.HashIP(h.ipKey, middleware.ClientIP(r, h.proxies))
	err := h.cadastro.ConfirmarEmail(r.Context(), req.Token, req.Password, ipHash)
	var travada auth.LockedError
	switch {
	case errors.Is(err, account.ErrTokenInvalido):
		apierr.WriteError(w, r, apierr.CodeTokenInvalid)
	case errors.Is(err, account.ErrInvalidCredentials):
		apierr.WriteError(w, r, apierr.CodeInvalidCredentials)
	case errors.As(err, &travada):
		w.Header().Set("Retry-After", strconv.Itoa(segundosAte(travada.Until, h.clk.Now())))
		apierr.WriteError(w, r, apierr.CodeRateLimited)
	case err != nil:
		h.internal(w, r, "confirmar e-mail", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ReenviarConfirmacao é POST /auth/email/resend, com sessão: 202 sem corpo.
// Com o e-mail já confirmado, também 202, e nenhum e-mail sai.
func (h *CadastroHandlers) ReenviarConfirmacao(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}
	err := h.cadastro.ReenviarConfirmacao(r.Context(), userID)
	switch {
	case errors.Is(err, account.ErrUserNotFound):
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
	case err != nil:
		h.internal(w, r, "reenviar confirmação", err)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

type esqueciASenhaRequest struct {
	Email          string `json:"email"`
	TurnstileToken string `json:"turnstile_token"`
}

// EsqueciASenha é POST /auth/password/forgot: 202 sem corpo, com conta ou sem
// conta. A ordem é a do Signup.
func (h *CadastroHandlers) EsqueciASenha(w http.ResponseWriter, r *http.Request) {
	if !h.config.Habilitado {
		apierr.WriteError(w, r, apierr.CodeNotFound)
		return
	}
	var req esqueciASenhaRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, err := account.ValidateEmail(req.Email); err != nil {
		apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "email", Reason: "invalid"}})
		return
	}
	if !h.desafioAceito(w, r, req.TurnstileToken, acaoForgot) {
		return
	}
	if err := h.recuperacao.PedirRedefinicao(r.Context(), req.Email); err != nil {
		h.internal(w, r, "pedir redefinição", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

type redefinirSenhaRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// RedefinirSenha é POST /auth/password/reset: 204, a senha trocada e todas as
// sessões encerradas. A política vem antes do token, como no serviço: um erro
// de digitação não gasta o link.
func (h *CadastroHandlers) RedefinirSenha(w http.ResponseWriter, r *http.Request) {
	var req redefinirSenhaRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if motivo := auth.ValidateNewPassword(req.NewPassword); motivo != "" {
		apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "new_password", Reason: motivo}})
		return
	}
	if !tokenDeContaPlausivel(req.Token) {
		apierr.WriteError(w, r, apierr.CodeTokenInvalid)
		return
	}
	err := h.recuperacao.Redefinir(r.Context(), req.Token, req.NewPassword)
	var pe account.PolicyError
	switch {
	case errors.Is(err, account.ErrTokenInvalido):
		apierr.WriteError(w, r, apierr.CodeTokenInvalid)
	case errors.As(err, &pe):
		apierr.WriteValidation(w, r, []apierr.FieldError{{Field: "new_password", Reason: pe.Reason}})
	case err != nil:
		h.internal(w, r, "redefinir senha", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// desafioAceito confere o Turnstile com o IP do cliente. Recusado é 400
// turnstile_failed; qualquer outra falha é 503 unavailable (falha fechada:
// sem siteverify, ninguém se cadastra). Devolve false quando já respondeu.
func (h *CadastroHandlers) desafioAceito(w http.ResponseWriter, r *http.Request, token, acao string) bool {
	err := h.turnstile.Verificar(r.Context(), token, middleware.ClientIP(r, h.proxies), acao)
	switch {
	case err == nil:
		return true
	case errors.Is(err, turnstile.ErrRecusado):
		apierr.WriteError(w, r, apierr.CodeTurnstileFailed)
	default:
		slog.ErrorContext(r.Context(), "turnstile: siteverify indisponível",
			"erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeUnavailable)
	}
	return false
}

// tokenDeContaPlausivel recusa sem consulta o token vazio ou comprido demais.
func tokenDeContaPlausivel(token string) bool {
	return token != "" && len(token) <= tamanhoMaximoDoTokenDeConta
}

// internal registra o erro real e devolve só o envelope genérico; o teto do
// Argon2id cheio é o 429 de argon2Ocupado.
func (h *CadastroHandlers) internal(w http.ResponseWriter, r *http.Request, op string, err error) {
	if errors.Is(err, auth.ErrArgon2Busy) {
		argon2Ocupado(w, r, op)
		return
	}
	erroInterno(w, r, op, err)
}

// segundosAte é o Retry-After até ate: o que falta, arredondado para cima, e
// nunca menos de 1 s (a trava que venceu entre a recusa e a resposta).
func segundosAte(ate, agora time.Time) int {
	falta := ate.Sub(agora)
	segundos := int((falta + time.Second - 1) / time.Second)
	return max(segundos, 1)
}
