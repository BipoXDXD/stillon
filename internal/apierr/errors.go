// Package apierr define o envelope de erro HTTP do StillOn.
//
// É um pacote folha de propósito: tanto internal/httpapi quanto
// internal/httpapi/middleware precisam de WriteError, e internal/httpapi
// (via routes.go, Task 8) importa internal/httpapi/middleware. Se WriteError
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
	CodeInvalidCredentials ErrorCode = "invalid_credentials"
	CodeInvalidSession     ErrorCode = "invalid_session"
	CodeUnauthorized       ErrorCode = "unauthorized"
	CodeNotFound           ErrorCode = "not_found"
	CodeValidation         ErrorCode = "validation_failed"
	CodeRateLimited        ErrorCode = "rate_limited"
	CodePayloadTooLarge    ErrorCode = "payload_too_large"
	CodeForbidden          ErrorCode = "forbidden"
	CodeInternal           ErrorCode = "internal_error"
	CodeUnavailable        ErrorCode = "unavailable"
	CodeChannelLimit       ErrorCode = "channel_limit_reached"
	CodeChannelNotPending  ErrorCode = "channel_not_pending"
	CodeDeliveryFailed     ErrorCode = "delivery_failed"
	CodeStatusPageExists   ErrorCode = "status_page_exists"
	CodeSlugTaken          ErrorCode = "slug_taken"
	CodeTargetLimit        ErrorCode = "target_limit_reached"
	CodeMethodNotAllowed   ErrorCode = "method_not_allowed"
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
	// Uma status page por usuário (spec §2.1); a segunda é conflito.
	CodeStatusPageExists: {http.StatusConflict, "Você já tem uma status page."},
	CodeSlugTaken:        {http.StatusConflict, "Este endereço já está em uso."},
	// O teto de alvos por usuário (D17 do Plano 5); reconciliado no contrato
	// dos Planos 5–7.
	CodeTargetLimit: {http.StatusConflict, "Limite de alvos atingido."},
	// O método não existe naquela rota (B5 do Plano 10). Nenhum código
	// anterior tinha 405: o not_found mudaria o status, e o Allow do ServeMux
	// perderia o sentido.
	CodeMethodNotAllowed: {http.StatusMethodNotAllowed, "Método não permitido nesta rota."},
}

// FieldError aponta o campo culpado de um 400 (D14). Field e Reason vêm de
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
