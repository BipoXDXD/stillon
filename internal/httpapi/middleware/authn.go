package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/apierr"
	"github.com/BipoXDXD/vigilume/internal/auth"
	"github.com/BipoXDXD/vigilume/internal/clock"
)

type userKey struct{}

// UserID recupera o usuário autenticado. O segundo retorno é false quando a
// rota não passou por Authn — um handler protegido deve tratar isso como 401,
// nunca assumir presença.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userKey{}).(uuid.UUID)
	return id, ok
}

// Authn exige um access token válido no header Authorization.
//
// O envelope vem de apierr, e não de httpapi, porque httpapi importa este
// pacote em routes.go — o caminho inverso fecharia um ciclo de importação.
func Authn(jwtSecret []byte, clk clock.Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				apierr.WriteError(w, r, apierr.CodeUnauthorized)
				return
			}

			userID, err := auth.ParseAccessToken(jwtSecret, raw, clk.Now())
			if err != nil {
				apierr.WriteError(w, r, apierr.CodeUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userKey{}, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
