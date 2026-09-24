package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/observability"
)

// Recover impede que um panic derrube o processo e, principalmente, que o
// stack trace chegue ao cliente: ele revela caminhos de arquivo, nomes de
// pacote e versões de dependência.
//
// O log leva o padrão da rota (r.Pattern), nunca r.URL.Path: em
// /verify/{token}, o caminho é um token de verificação ainda válido. O
// ServeMux preenche r.Pattern na própria requisição que recebe, então um
// Recover por fora da cadeia vê o padrão do mux mais interno que tocou nela
// (ou vazio, se uma cópia via WithContext ficou no meio).
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic no handler",
					"trace_id", observability.TraceID(r.Context()),
					"metodo", r.Method,
					"rota", r.Pattern,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				apierr.WriteError(w, r, apierr.CodeInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
