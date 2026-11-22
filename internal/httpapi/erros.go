package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// erroInterno é o fim de todo caminho que falhou por dentro: o erro real vai
// só para o log, correlacionado pelo trace_id, e o cliente recebe apenas o
// envelope genérico internal_error com o mesmo trace_id (spec §9.1). Quem
// chama decide antes o que não é erro interno (domínio, Argon2id ocupado).
// Quando o próprio cliente desistiu (a requisição foi cancelada e o erro é
// esse cancelamento), não há falha do servidor: o registro desce para info.
// Um context.Canceled com a requisição ainda viva veio de outra fonte e
// continua sendo erro.
func erroInterno(w http.ResponseWriter, r *http.Request, op string, err error) {
	traceID := observability.TraceID(r.Context())
	if errors.Is(err, context.Canceled) && errors.Is(r.Context().Err(), context.Canceled) {
		slog.Info(op+": cliente desistiu", "erro", err, "trace_id", traceID)
	} else {
		slog.Error(op, "erro", err, "trace_id", traceID)
	}
	apierr.WriteError(w, r, apierr.CodeInternal)
}
