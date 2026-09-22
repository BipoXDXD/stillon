package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/BipoXDXD/sentinela/internal/apierr"
	"github.com/BipoXDXD/sentinela/internal/observability"
)

// Recover impede que um panic derrube o processo e, principalmente, que o
// stack trace chegue ao cliente: ele revela caminhos de arquivo, nomes de
// pacote e versões de dependência.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic no handler",
					"trace_id", observability.TraceID(r.Context()),
					"rota", r.URL.Path,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				apierr.WriteError(w, r, apierr.CodeInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
