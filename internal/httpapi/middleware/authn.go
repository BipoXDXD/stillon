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

type familyKey struct{}

// UserID recupera o usuário autenticado. O segundo retorno é false quando a
// rota não passou por Authn — um handler protegido deve tratar isso como 401,
// nunca assumir presença.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userKey{}).(uuid.UUID)
	return id, ok
}

// FamilyID recupera a família de sessão do access token (o claim sid). Mesmo
// contrato de UserID: false quando a rota não passou por Authn.
func FamilyID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(familyKey{}).(uuid.UUID)
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

			claims, err := auth.ParseAccessToken(jwtSecret, raw, clk.Now())
			if err != nil {
				apierr.WriteError(w, r, apierr.CodeUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userKey{}, claims.UserID)
			ctx = context.WithValue(ctx, familyKey{}, claims.FamilyID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
