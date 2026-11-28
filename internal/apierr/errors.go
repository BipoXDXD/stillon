// Package apierr define o envelope de erro HTTP do StillOn.
//
// É um pacote folha de propósito: tanto internal/httpapi quanto
// internal/httpapi/middleware precisam de WriteError, e internal/httpapi
// (via routes.go) importa internal/httpapi/middleware. Se WriteError
// vivesse em internal/httpapi, os middlewares teriam que importar
// internal/httpapi para usá-lo, e internal/httpapi importaria de volta
// internal/httpapi/middleware — ciclo de importação, que Go proíbe. Por isso
// esse código mora aqui, num pacote que ambos os lados podem importar sem
// se importarem entre si.
package apierr

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/stillon/internal/observability"
)

type ErrorCode string

const (
	// #nosec G101 -- o regex do gosec bate no NOME da constante ("Credentials"); o valor é um código de erro da API, não um segredo.
	CodeInvalidCredentials  ErrorCode = "invalid_credentials"
	CodeInvalidSession      ErrorCode = "invalid_session"
	CodeUnauthorized        ErrorCode = "unauthorized"
	CodeNotFound            ErrorCode = "not_found"
	CodeValidation          ErrorCode = "validation_failed"
	CodeRateLimited         ErrorCode = "rate_limited"
	CodePayloadTooLarge     ErrorCode = "payload_too_large"
	CodeForbidden           ErrorCode = "forbidden"
	CodeInternal            ErrorCode = "internal_error"
	CodeUnavailable         ErrorCode = "unavailable"
	CodeChannelLimit        ErrorCode = "channel_limit_reached"
	CodeChannelNotPending   ErrorCode = "channel_not_pending"
	CodeDeliveryFailed      ErrorCode = "delivery_failed"
	CodeStatusPageExists    ErrorCode = "status_page_exists"
	CodeSlugTaken           ErrorCode = "slug_taken"
	CodeTargetLimit         ErrorCode = "target_limit_reached"
	CodeHeartbeatLimit      ErrorCode = "heartbeat_limit_reached"
	CodeContentHostLimit    ErrorCode = "content_rule_host_limit_reached"
	CodeMethodNotAllowed    ErrorCode = "method_not_allowed"
	CodeScheduleUnsupported ErrorCode = "schedule_unsupported"
	CodeProjectLimit        ErrorCode = "project_limit_reached"
	CodeEmailNotVerified    ErrorCode = "email_not_verified"
	CodeTurnstileFailed     ErrorCode = "turnstile_failed"
	CodeTitularProtected    ErrorCode = "titular_protected"
	CodeMemberLimit         ErrorCode = "member_limit_reached"
	CodeAlreadyMember       ErrorCode = "already_member"
	CodeInvitationOther     ErrorCode = "invitation_other_account"
	CodeTransferTarget      ErrorCode = "titular_transfer_target"
	// #nosec G101 -- o regex do gosec bate no NOME da constante ("Token"); o valor é um código de erro da API, não um segredo.
	CodeTokenInvalid ErrorCode = "token_invalid"
)

// A mensagem vem sempre desta tabela, nunca do erro subjacente.
//
// Essa é a garantia estrutural da spec §9.1: como WriteError não aceita um
// error como parâmetro, é impossível vazar "pq: duplicate key value violates
// unique constraint" para o cliente, mesmo por descuido.
var errorTable = map[ErrorCode]struct {
	status  int
	message string
}{
	CodeInvalidCredentials: {http.StatusUnauthorized, "E-mail ou senha incorretos."},
	CodeInvalidSession:     {http.StatusUnauthorized, "Sessão inválida ou expirada."},
	CodeUnauthorized:       {http.StatusUnauthorized, "Autenticação necessária."},
	CodeNotFound:           {http.StatusNotFound, "Recurso não encontrado."},
	CodeValidation:         {http.StatusBadRequest, "Dados inválidos."},
	CodeRateLimited:        {http.StatusTooManyRequests, "Muitas requisições."},
	CodePayloadTooLarge:    {http.StatusRequestEntityTooLarge, "Corpo da requisição grande demais."},
	CodeForbidden:          {http.StatusForbidden, "Operação não permitida."},
	CodeInternal:           {http.StatusInternalServerError, "Erro interno."},
	CodeUnavailable:        {http.StatusServiceUnavailable, "Serviço indisponível."},
	CodeChannelLimit:       {http.StatusConflict, "Limite de canais atingido."},
	CodeChannelNotPending:  {http.StatusConflict, "O canal não está pendente de verificação."},
	// 502: a falha foi do destino (Discord ou provedor de e-mail), não do
	// pedido nem deste servidor.
	CodeDeliveryFailed: {http.StatusBadGateway, "Não foi possível entregar a mensagem de verificação."},
	// Uma status page por projeto (00046); a segunda no mesmo projeto é
	// conflito.
	CodeStatusPageExists: {http.StatusConflict, "Este projeto já tem uma status page."},
	CodeSlugTaken:        {http.StatusConflict, "Este endereço já está em uso."},
	// O teto de alvos por titular, somando os projetos dele.
	CodeTargetLimit:    {http.StatusConflict, "Limite de alvos atingido."},
	CodeHeartbeatLimit: {http.StatusConflict, "Limite de heartbeats atingido."},
	// Sem o host nem a contagem: o painel já sabe o host que o dono digitou.
	CodeContentHostLimit: {http.StatusConflict, "Limite de regras de conteúdo para este host atingido."},
	// O método não existe naquela rota. Nenhum código
	// anterior tinha 405: o not_found mudaria o status, e o Allow do ServeMux
	// perderia o sentido.
	CodeMethodNotAllowed: {http.StatusMethodNotAllowed, "Método não permitido nesta rota."},
	// O heartbeat foi gravado por uma versão posterior, com um agendamento que
	// esta não sabe editar: conflito com o estado do recurso, não pedido inválido.
	CodeScheduleUnsupported: {http.StatusConflict, "Agendamento não suportado nesta versão."},
	// O teto de projetos de que a conta é titular (o padrão conta).
	CodeProjectLimit: {http.StatusConflict, "Limite de projetos atingido."},
	// A conta do cadastro público ainda não confirmou o e-mail e por isso não
	// cria recurso. Separado de forbidden para o painel mostrar "confirme seu
	// e-mail", e não "sem permissão".
	CodeEmailNotVerified: {http.StatusForbidden, "Confirme seu e-mail para criar recursos."},
	// O desafio do Turnstile foi recusado. Sai antes de qualquer consulta ao
	// banco: não diz nada sobre o e-mail.
	CodeTurnstileFailed: {http.StatusBadRequest, "Não foi possível confirmar que você não é um robô."},
	// Token de confirmação ou de redefinição inexistente, já usado ou vencido:
	// a mesma resposta para os três.
	CodeTokenInvalid: {http.StatusBadRequest, "Link inválido ou expirado."},
	// O titular é sempre owner (FK da 00048): não sai, não é removido e não
	// perde o papel. Conflito com o estado do projeto; a troca de titular é
	// a transferência, em dois passos.
	CodeTitularProtected: {http.StatusConflict, "O titular do projeto não pode sair nem perder o papel de owner."},
	// O teto de membros do titular, contando os convites pendentes.
	CodeMemberLimit: {http.StatusConflict, "Limite de membros atingido."},
	// Convidar quem já está na equipe: quem convida já vê a lista de membros,
	// então o código não enumera ninguém.
	CodeAlreadyMember: {http.StatusConflict, "Este e-mail já é membro do projeto."},
	// O link é válido, mas o convite é de outro e-mail. Só sai depois de o
	// token se provar válido, e o texto não mostra o e-mail do convite.
	CodeInvitationOther: {http.StatusForbidden, "Este convite é de outra conta. Entre com o e-mail que recebeu o convite."},
	// A titularidade só vai para outro owner do projeto, e o Projeto padrão
	// não é transferido (PR 4.3).
	CodeTransferTarget: {http.StatusConflict, "A titularidade só pode ser transferida para outro owner, e o Projeto padrão não é transferido."},
}

// FieldError aponta o campo culpado de um 400. Field e Reason vêm de
// vocabulário fechado, definido no código e nunca no corpo recebido: é o que
// mantém a garantia de WriteError de que nada do cliente nem do banco ecoa
// na resposta.
type FieldError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type errorBody struct {
	Error struct {
		Code    ErrorCode    `json:"code"`
		Message string       `json:"message"`
		Fields  []FieldError `json:"fields,omitempty"`
	} `json:"error"`
	TraceID string `json:"trace_id"`
}

func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode) {
	writeError(w, r, code, nil)
}

// WriteValidation é o 400 com os campos culpados. Extensão compatível da
// spec §9.1: quem ignora "fields" continua lendo code e message.
func WriteValidation(w http.ResponseWriter, r *http.Request, fields []FieldError) {
	writeError(w, r, CodeValidation, fields)
}

func writeError(w http.ResponseWriter, r *http.Request, code ErrorCode, fields []FieldError) {
	entry, known := errorTable[code]
	if !known {
		entry = errorTable[CodeInternal]
		code = CodeInternal
	}
	// O prazo vencido representa indisponibilidade temporária. Erros de
	// domínio continuam com seus códigos, e nenhum erro SQL é exposto.
	if code == CodeInternal && errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		code = CodeUnavailable
		entry = errorTable[code]
	}

	var body errorBody
	body.Error.Code = code
	body.Error.Message = entry.message
	body.Error.Fields = fields
	body.TraceID = observability.TraceID(r.Context())

	WriteJSON(w, entry.status, body)
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("falha ao escrever resposta", "erro", err)
	}
}
