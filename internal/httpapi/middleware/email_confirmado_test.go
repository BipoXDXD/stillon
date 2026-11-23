package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/httpapi/middleware"
)

// conferidorFalso responde pelo usuário que o Authn pôs no contexto: só o
// confirmado é confirmado; falha, se houver, sai em toda chamada.
type conferidorFalso struct {
	confirmado uuid.UUID
	falha      error
	visto      uuid.UUID
}

func (c *conferidorFalso) EmailConfirmado(_ context.Context, userID uuid.UUID) (bool, error) {
	c.visto = userID
	if c.falha != nil {
		return false, c.falha
	}
	return userID == c.confirmado, nil
}

// proximoVisto marca se a cadeia chegou ao handler final.
type proximoVisto struct{ chegou bool }

func (p *proximoVisto) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	p.chegou = true
	w.WriteHeader(http.StatusCreated)
}

// pedirCriacao monta RequestID → Authn → ExigeEmailConfirmado → final, a
// ordem das rotas reais, e faz um POST com o token do usuário.
func pedirCriacao(t *testing.T, c middleware.ConferidorDeEmail, final http.Handler, usuario uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	authn := middleware.Authn(segredoDoProjeto, clock.NewFake(agoraDoProjeto))
	h := middleware.RequestID(authn(middleware.ExigeEmailConfirmado(c)(final)))
	tok, err := auth.NewAccessToken(segredoDoProjeto, usuario, uuid.New(), agoraDoProjeto)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/coisas", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestExigeEmailConfirmadoDeixaPassarConfirmado(t *testing.T) {
	usuario := uuid.New()
	conf := &conferidorFalso{confirmado: usuario}
	final := &proximoVisto{}
	rec := pedirCriacao(t, conf, final, usuario)
	if rec.Code != http.StatusCreated || !final.chegou {
		t.Fatalf("status %d, chegou ao próximo = %v; queria 201 e true: %s", rec.Code, final.chegou, rec.Body.String())
	}
	if conf.visto != usuario {
		t.Errorf("o conferidor recebeu %v, queria o usuário do token %v", conf.visto, usuario)
	}
}

func TestExigeEmailConfirmadoRecusaNaoConfirmado(t *testing.T) {
	final := &proximoVisto{}
	rec := pedirCriacao(t, &conferidorFalso{confirmado: uuid.New()}, final, uuid.New())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, queria 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"email_not_verified"`) {
		t.Errorf("corpo sem email_not_verified: %s", rec.Body.String())
	}
	if final.chegou {
		t.Error("o próximo handler rodou para conta não confirmada")
	}
}

func TestExigeEmailConfirmadoErroDa500(t *testing.T) {
	usuario := uuid.New()
	final := &proximoVisto{}
	rec := pedirCriacao(t, &conferidorFalso{confirmado: usuario, falha: errors.New("banco fora")}, final, usuario)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"internal_error"`) {
		t.Fatalf("status %d, queria 500 internal_error: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "banco fora") {
		t.Errorf("o envelope vazou o erro: %s", rec.Body.String())
	}
	if final.chegou {
		t.Error("o próximo handler rodou com o conferidor em erro")
	}
}

// Sem usuário no contexto (a rota montada sem Authn antes), a trava não deixa
// passar: 401, como os outros middlewares que dependem do Authn.
func TestExigeEmailConfirmadoSemUsuarioDa401(t *testing.T) {
	final := &proximoVisto{}
	h := middleware.RequestID(middleware.ExigeEmailConfirmado(&conferidorFalso{})(final))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/coisas", nil))
	if rec.Code != http.StatusUnauthorized || final.chegou {
		t.Fatalf("status %d, chegou = %v; queria 401 e false", rec.Code, final.chegou)
	}
}
