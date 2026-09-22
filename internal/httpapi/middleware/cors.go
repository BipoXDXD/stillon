package middleware

import "net/http"

// CORS libera exatamente uma origem. Curinga é proibido pela spec §5.7, e
// com credenciais o navegador nem aceitaria.
func CORS(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// Vary é incondicional (R33): a resposta depende do header Origin
			// nos dois ramos, não só quando ele bate com o configurado. Sem
			// isso no ramo negativo, um cache poderia servir a um cliente
			// legítimo a resposta (sem headers de CORS) de uma requisição
			// hostil anterior feita para a mesma URL.
			h.Add("Vary", "Origin")
			if r.Header.Get("Origin") == origin {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
