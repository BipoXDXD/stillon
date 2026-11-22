package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/observability"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

type projectKey struct{}

// ResolvedorDeProjeto confere a pertença do usuário e devolve o escopo dele.
// tenancy.Service o implementa; os testes passam um falso.
type ResolvedorDeProjeto interface {
	// Scope devolve tenancy.ErrProjectNotFound para não membro, projeto
	// inexistente e suspensão: os três têm de ser indistinguíveis.
	Scope(ctx context.Context, userID, projectID uuid.UUID) (tenancy.ProjectScope, error)
	// DefaultScope garante o Projeto padrão do usuário e devolve o escopo nele.
	DefaultScope(ctx context.Context, userID uuid.UUID) (tenancy.ProjectScope, error)
}

// Projeto recupera o escopo que ProjetoDoCaminho ou ProjetoPadrao pôs no
// contexto. Mesmo contrato de UserID: false quando a rota não passou por eles.
func Projeto(ctx context.Context) (tenancy.ProjectScope, bool) {
	sc, ok := ctx.Value(projectKey{}).(tenancy.ProjectScope)
	return sc, ok
}

// ProjetoDoCaminho resolve o {projectID} da rota. Vem depois de Authn.
//
// Id malformado, projeto inexistente e projeto de que o usuário não é membro
// dão o MESMO 404 not_found: um 403, ou um 404 diferente, diria a quem não é
// membro que o projeto existe.
func ProjetoDoCaminho(res ResolvedorDeProjeto) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserID(r.Context())
			if !ok {
				apierr.WriteError(w, r, apierr.CodeUnauthorized)
				return
			}
			projectID, err := uuid.Parse(r.PathValue("projectID"))
			if err != nil {
				apierr.WriteError(w, r, apierr.CodeNotFound)
				return
			}
			sc, err := res.Scope(r.Context(), userID, projectID)
			seguirComEscopo(w, r, next, sc, err, "resolver projeto da rota")
		})
	}
}

// ProjetoPadrao põe no contexto o Projeto padrão do usuário, criando-o na
// primeira vez: é o projeto das rotas antigas, sem {projectID}. Vem depois de
// Authn.
func ProjetoPadrao(res ResolvedorDeProjeto) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserID(r.Context())
			if !ok {
				apierr.WriteError(w, r, apierr.CodeUnauthorized)
				return
			}
			sc, err := res.DefaultScope(r.Context(), userID)
			seguirComEscopo(w, r, next, sc, err, "resolver Projeto padrão")
		})
	}
}

// seguirComEscopo traduz o resultado do resolvedor: sem pertença é 404, outra
// falha é 500 com o erro real só no log (o envelope nunca leva o motivo).
func seguirComEscopo(w http.ResponseWriter, r *http.Request, next http.Handler, sc tenancy.ProjectScope, err error, op string) {
	switch {
	case errors.Is(err, tenancy.ErrProjectNotFound):
		apierr.WriteError(w, r, apierr.CodeNotFound)
	case err != nil:
		slog.Error(op, "erro", err, "trace_id", observability.TraceID(r.Context()))
		apierr.WriteError(w, r, apierr.CodeInternal)
	default:
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), projectKey{}, sc)))
	}
}
