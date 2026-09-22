package middleware

import "net/http"

// BodyLimit envolve o corpo com http.MaxBytesReader (spec §5.7). Sem isso,
// uma requisição com corpo gigante consome memória até derrubar o processo.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
