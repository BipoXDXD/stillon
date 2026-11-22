package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

var (
	segredoDoProjeto = []byte("segredo-de-teste-com-32-bytes!!!")
	agoraDoProjeto   = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
)

// resolvedorFalso conhece um vínculo só (membro em projeto) e um Projeto
// padrão; falha devolve esse erro em toda chamada.
type resolvedorFalso struct {
	membro, projeto uuid.UUID
	padrao          tenancy.ProjectScope
	falha           error
}

func (f resolvedorFalso) Scope(_ context.Context, userID, projectID uuid.UUID) (tenancy.ProjectScope, error) {
	if f.falha != nil {
		return tenancy.ProjectScope{}, f.falha
	}
	if userID != f.membro || projectID != f.projeto {
		return tenancy.ProjectScope{}, tenancy.ErrProjectNotFound
	}
	return tenancy.ProjectScope{ProjectID: projectID, UserID: userID, Role: tenancy.RoleMember}, nil
}

func (f resolvedorFalso) DefaultScope(_ context.Context, userID uuid.UUID) (tenancy.ProjectScope, error) {
	if f.falha != nil {
		return tenancy.ProjectScope{}, f.falha
	}
	if userID != f.padrao.UserID {
		return tenancy.ProjectScope{}, tenancy.ErrProjectNotFound
	}
	return f.padrao, nil
}

// escopoVisto é o handler final: guarda o escopo que o middleware pôs no
// contexto, se pôs.
type escopoVisto struct {
	chegou bool
	escopo tenancy.ProjectScope
	ok     bool
}

func (e *escopoVisto) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.chegou = true
	e.escopo, e.ok = middleware.Projeto(r.Context())
	w.WriteHeader(http.StatusOK)
}

// cadeiaDeProjeto monta RequestID → Authn → middleware de projeto → final,
// com o {projectID} do ServeMux, como nas rotas reais.
func cadeiaDeProjeto(mw func(http.Handler) http.Handler, final http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /projetos/{projectID}/coisas", mw(final))
	mux.Handle("GET /coisas", mw(final))
	authn := middleware.Authn(segredoDoProjeto, clock.NewFake(agoraDoProjeto))
	return middleware.RequestID(authn(mux))
}

func tokenDoProjeto(t *testing.T, usuario uuid.UUID) string {
	t.Helper()
	tok, err := auth.NewAccessToken(segredoDoProjeto, usuario, uuid.New(), agoraDoProjeto)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func pedirProjeto(h http.Handler, caminho, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, caminho, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// corpoSemTrace decodifica o envelope de erro e tira o trace_id, o único campo
// que muda de um pedido para outro.
func corpoSemTrace(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var corpo map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("corpo não é JSON: %v: %s", err, rec.Body.String())
	}
	if corpo["trace_id"] == "" || corpo["trace_id"] == nil {
		t.Fatalf("o envelope saiu sem trace_id: %s", rec.Body.String())
	}
	delete(corpo, "trace_id")
	return corpo
}

// Quem não é membro não pode distinguir um projeto alheio de um inexistente,
// nem de um id malformado: as três respostas são o mesmo 404.
func TestProjetoDoCaminhoMapsBadUUIDAndNonMemberToSame404(t *testing.T) {
	membro, estranho, projeto := uuid.New(), uuid.New(), uuid.New()
	final := &escopoVisto{}
	h := cadeiaDeProjeto(middleware.ProjetoDoCaminho(resolvedorFalso{membro: membro, projeto: projeto}), final)

	casos := []struct {
		nome, caminho string
		usuario       uuid.UUID
	}{
		{"id malformado", "/projetos/nao-e-uuid/coisas", membro},
		{"projeto inexistente", "/projetos/" + uuid.NewString() + "/coisas", membro},
		{"projeto de outro", "/projetos/" + projeto.String() + "/coisas", estranho},
	}
	var referencia map[string]any
	for _, c := range casos {
		rec := pedirProjeto(h, c.caminho, tokenDoProjeto(t, c.usuario))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, queria 404: %s", c.nome, rec.Code, rec.Body.String())
		}
		corpo := corpoSemTrace(t, rec)
		if erro, _ := corpo["error"].(map[string]any); erro["code"] != "not_found" {
			t.Fatalf("%s: corpo %v, queria code not_found", c.nome, corpo)
		}
		if referencia == nil {
			referencia = corpo
			continue
		}
		a, _ := json.Marshal(referencia)
		b, _ := json.Marshal(corpo)
		if string(a) != string(b) {
			t.Errorf("%s: corpo %s difere de %s", c.nome, b, a)
		}
	}
	if final.chegou {
		t.Error("o handler rodou sem pertença conferida")
	}
}

func TestProjetoDoCaminhoPutsScopeInContext(t *testing.T) {
	membro, projeto := uuid.New(), uuid.New()
	final := &escopoVisto{}
	h := cadeiaDeProjeto(middleware.ProjetoDoCaminho(resolvedorFalso{membro: membro, projeto: projeto}), final)

	rec := pedirProjeto(h, "/projetos/"+projeto.String()+"/coisas", tokenDoProjeto(t, membro))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	quer := tenancy.ProjectScope{ProjectID: projeto, UserID: membro, Role: tenancy.RoleMember}
	if !final.ok || final.escopo != quer {
		t.Errorf("escopo no contexto = %+v (ok=%v), queria %+v", final.escopo, final.ok, quer)
	}
}

func TestProjetoPadraoResolvesDefault(t *testing.T) {
	usuario := uuid.New()
	padrao := tenancy.ProjectScope{ProjectID: uuid.New(), UserID: usuario, Role: tenancy.RoleOwner}
	final := &escopoVisto{}
	h := cadeiaDeProjeto(middleware.ProjetoPadrao(resolvedorFalso{padrao: padrao}), final)

	rec := pedirProjeto(h, "/coisas", tokenDoProjeto(t, usuario))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !final.ok || final.escopo != padrao {
		t.Errorf("escopo no contexto = %+v (ok=%v), queria %+v", final.escopo, final.ok, padrao)
	}
}

// Sem Authn antes, os dois middlewares recusam com 401: nunca resolvem
// projeto para um usuário zero.
func TestProjectMiddlewareWithoutUserIs401(t *testing.T) {
	res := resolvedorFalso{membro: uuid.Nil, projeto: uuid.Nil, padrao: tenancy.ProjectScope{}}
	for nome, mw := range map[string]func(http.Handler) http.Handler{
		"ProjetoDoCaminho": middleware.ProjetoDoCaminho(res),
		"ProjetoPadrao":    middleware.ProjetoPadrao(res),
	} {
		t.Run(nome, func(t *testing.T) {
			final := &escopoVisto{}
			mux := http.NewServeMux()
			mux.Handle("GET /projetos/{projectID}/coisas", mw(final))
			rec := pedirProjeto(middleware.RequestID(mux), "/projetos/"+uuid.Nil.String()+"/coisas", "")
			if rec.Code != http.StatusUnauthorized || !json.Valid(rec.Body.Bytes()) {
				t.Fatalf("status %d: %s, queria 401", rec.Code, rec.Body.String())
			}
			if erro, _ := corpoSemTrace(t, rec)["error"].(map[string]any); erro["code"] != "unauthorized" {
				t.Errorf("corpo %s, queria code unauthorized", rec.Body.String())
			}
			if final.chegou {
				t.Error("o handler rodou sem usuário")
			}
		})
	}
}

// Uma falha que não é "sem pertença" (banco fora, por exemplo) é 500, e não
// um 404 que esconderia a pane.
func TestProjectMiddlewareOtherErrorIs500(t *testing.T) {
	res := resolvedorFalso{falha: errors.New("banco fora")}
	for nome, c := range map[string]struct {
		mw      func(http.Handler) http.Handler
		caminho string
	}{
		"ProjetoDoCaminho": {middleware.ProjetoDoCaminho(res), "/projetos/" + uuid.NewString() + "/coisas"},
		"ProjetoPadrao":    {middleware.ProjetoPadrao(res), "/coisas"},
	} {
		t.Run(nome, func(t *testing.T) {
			final := &escopoVisto{}
			rec := pedirProjeto(cadeiaDeProjeto(c.mw, final), c.caminho, tokenDoProjeto(t, uuid.New()))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status %d: %s, queria 500", rec.Code, rec.Body.String())
			}
			if erro, _ := corpoSemTrace(t, rec)["error"].(map[string]any); erro["code"] != "internal_error" {
				t.Errorf("corpo %s, queria code internal_error", rec.Body.String())
			}
			if final.chegou {
				t.Error("o handler rodou sem escopo")
			}
		})
	}
}
