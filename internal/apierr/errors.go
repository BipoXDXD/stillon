// Package apierr define o envelope de erro HTTP do Sentinela.
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
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/sentinela/internal/observability"
)

type ErrorCode string

const (
	CodeInvalidCredentials ErrorCode = "invalid_credentials"
	CodeInvalidSession     ErrorCode = "invalid_session"
	CodeUnauthorized       ErrorCode = "unauthorized"
	CodeAccountLocked      ErrorCode = "account_locked"
	CodeNotFound           ErrorCode = "not_found"
	CodeValidation         ErrorCode = "validation_failed"
	CodeRateLimited        ErrorCode = "rate_limited"
	CodePayloadTooLarge    ErrorCode = "payload_too_large"
	CodeForbidden          ErrorCode = "forbidden"
	CodeInternal           ErrorCode = "internal_error"
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
	CodeAccountLocked:      {http.StatusTooManyRequests, "Conta temporariamente bloqueada."},
	CodeNotFound:           {http.StatusNotFound, "Recurso não encontrado."},
	CodeValidation:         {http.StatusBadRequest, "Dados inválidos."},
	CodeRateLimited:        {http.StatusTooManyRequests, "Muitas requisições."},
	CodePayloadTooLarge:    {http.StatusRequestEntityTooLarge, "Corpo da requisição grande demais."},
	CodeForbidden:          {http.StatusForbidden, "Operação não permitida."},
	CodeInternal:           {http.StatusInternalServerError, "Erro interno."},
}

type errorBody struct {
	Error struct {
		Code    ErrorCode `json:"code"`
		Message string    `json:"message"`
	} `json:"error"`
	TraceID string `json:"trace_id"`
}

func WriteError(w http.ResponseWriter, r *http.Request, code ErrorCode) {
	entry, known := errorTable[code]
	if !known {
		entry = errorTable[CodeInternal]
		code = CodeInternal
	}

	var body errorBody
	body.Error.Code = code
	body.Error.Message = entry.message
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
