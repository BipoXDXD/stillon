package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// erroInterno é o fim de todo caminho que falhou por dentro: o erro real vai
// só para o log, correlacionado pelo trace_id, e o cliente recebe apenas o
// envelope genérico internal_error com o mesmo trace_id (spec §9.1). Quem
// chama decide antes o que não é erro interno (domínio, Argon2id ocupado).
func erroInterno(w http.ResponseWriter, r *http.Request, op string, err error) {
	slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
	apierr.WriteError(w, r, apierr.CodeInternal)
}
