package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// escopo é como um handler de recurso lê o projeto em que age. Sem escopo no
// contexto a rota foi montada sem ProjetoDoCaminho nem ProjetoPadrao: defeito
// de montagem, que falha fechado com 500 em vez de seguir num escopo zero.
// Com ok false, a resposta já foi escrita.
func escopo(w http.ResponseWriter, r *http.Request) (tenancy.ProjectScope, bool) {
	sc, ok := middleware.Projeto(r.Context())
	if !ok {
		slog.Error("rota de recurso sem escopo de projeto",
			"rota", r.Pattern, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
		return tenancy.ProjectScope{}, false
	}
	return sc, true
}
