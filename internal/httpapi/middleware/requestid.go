// Package middleware reúne os middlewares HTTP do Sentinela, um por arquivo
// para que cada um seja testado e revisado isoladamente.
package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/observability"
)

const TraceHeader = "X-Trace-Id"

// RequestID atribui um identificador a cada requisição e o devolve no header.
// É o que liga a mensagem genérica vista pelo cliente ao log detalhado do
// servidor (spec §9.1).
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.NewV7()
		trace := id.String()
		if err != nil {
			trace = "sem-trace"
		}
		w.Header().Set(TraceHeader, trace)
		next.ServeHTTP(w, r.WithContext(observability.WithTraceID(r.Context(), trace)))
	})
}
