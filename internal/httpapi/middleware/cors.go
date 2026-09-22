package middleware

import "net/http"

// CORS libera exatamente uma origem. Curinga é proibido pela spec §5.7, e
// com credenciais o navegador nem aceitaria.
//
// CORS cuida APENAS dos cabeçalhos, e sempre chama next. O atalho do
// preflight mora em Preflight(), mais para dentro da cadeia. A divisão é
// deliberada (item 24): o preflight precisa contar no rate limit, mas o
// limitador não pode ficar por fora dos cabeçalhos de CORS. Se ele ficasse,
// TODA resposta 429 sairia sem Access-Control-Allow-Origin e sem
// Vary: Origin — o SPA legítimo veria um erro opaco, incapaz de ler a
// própria recusa, e a perda do Vary reabriria o buraco de cache que a
// ruling R33 fechou.
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
			next.ServeHTTP(w, r)
		})
	}
}

// Preflight responde 204 ao OPTIONS e para a cadeia ali. É a outra metade do
// antigo CORS: por ficar DENTRO do rate limit (ver routes.go), o preflight
// passa a ser contado pelo limitador, em vez de atravessá-lo de graça. Os
// cabeçalhos de CORS já foram postos por CORS(), mais para fora, então tanto
// este 204 quanto um eventual 429 do limitador saem com eles (R33).
func Preflight() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
