package middleware

import (
	"net/http"

	"github.com/BipoXDXD/stillon/internal/apierr"
)

// CustomHeaderName é o header que toda mutação precisa enviar.
const CustomHeaderName = "X-Requested-With"

// RequireCustomHeader é a segunda camada anti-CSRF da spec §5.3.
//
// O refresh vive em cookie SameSite=Strict, o que já cobre o caso principal.
// Este header cobre o resto: um <form> em site hostil consegue disparar POST
// com o cookie junto, mas não consegue definir header customizado sem passar
// pelo preflight de CORS — que a origem hostil não passa.
func RequireCustomHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get(CustomHeaderName) == "" {
				apierr.WriteError(w, r, apierr.CodeForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
