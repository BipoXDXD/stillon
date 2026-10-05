package middleware

import (
	"context"
	"net/http"
	"time"
)

// Deadline limita o trabalho que observa o contexto, inclusive a aquisição
// de conexão e as consultas pgx. Mantém o handler na mesma goroutine e
// preserva um prazo menor do cliente. Não interrompe trabalho de CPU nem
// desfaz uma transação já confirmada.
func Deadline(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
