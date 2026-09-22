package middleware

import "net/http"

// SecurityHeaders aplica os cabeçalhos da spec §5.7.
//
// A CSP não tem unsafe-inline de propósito. O painel React é servido
// separadamente; esta API responde JSON, então a política pode ser a mais
// restritiva possível.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("Content-Security-Policy",
			"default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		// Item 26: nada que sai desta API deve ser guardado. A resposta de
		// POST já não é cacheável por padrão, então isto é defesa em
		// profundidade — cobre o histórico do navegador (que guarda respostas
		// fora das regras de cache) e o proxy mal-comportado que ignora o
		// método. Fica aqui, e não em apierr.WriteJSON, porque daqui cobre de
		// uma vez a resposta com access token, os envelopes de erro e o /me.
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
