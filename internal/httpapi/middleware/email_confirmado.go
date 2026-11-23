package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/account"
	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// ConferidorDeEmail diz se o usuário já confirmou o e-mail. account.Service o
// implementa; os testes passam um falso.
type ConferidorDeEmail interface {
	EmailConfirmado(ctx context.Context, userID uuid.UUID) (bool, error)
}

// ExigeEmailConfirmado é a trava "sem e-mail confirmado não cria": a conta do
// cadastro público, antes do link de confirmação, recebe 403
// email_not_verified em toda rota de criação. Vem depois de Authn (lê o
// usuário do contexto) e confere a coluna a cada pedido, então a confirmação
// vale na hora, sem sessão nova. Usuário que não existe mais é 401; outra
// falha da conferência é 500 com o erro só no log: na dúvida, não cria.
func ExigeEmailConfirmado(c ConferidorDeEmail) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserID(r.Context())
			if !ok {
				apierr.WriteError(w, r, apierr.CodeUnauthorized)
				return
			}
			confirmado, err := c.EmailConfirmado(r.Context(), userID)
			switch {
			case errors.Is(err, account.ErrUserNotFound):
				// Usuário apagado depois da conferência da sessão: 401, como o
				// /me, para o painel voltar ao login.
				apierr.WriteError(w, r, apierr.CodeUnauthorized)
			case err != nil:
				slog.Error("conferir e-mail confirmado", "erro", err, "trace_id", observability.TraceID(r.Context()))
				apierr.WriteError(w, r, apierr.CodeInternal)
			case !confirmado:
				apierr.WriteError(w, r, apierr.CodeEmailNotVerified)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
