package httpapi_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A cadeia das rotas novas do Plano 5, pelo Routes de verdade (a lição do
// Plano 1: provar cada middleware à mão não prova o wiring).
//
// Sem token, toda rota nova responde o 401 unauthorized. Duas guardas dão
// isso: o Authn da cadeia e o próprio handler, que recusa sem usuário no
// contexto (como o D25 do Plano 4); registrar a rota sem Authn deixa este
// teste verde, e o comentário existe para ninguém achar que ele trava o
// Authn sozinho.
func TestPlano5RoutesRequireAuthentication(t *testing.T) {
	a := newAPI(t)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodPost, "/api/v1/auth/password"},
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodDelete, "/api/v1/sessions/" + uuid.NewString()},
		{http.MethodGet, "/api/v1/targets/summary"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", nil)
		codigo := decodeEnvelope(t, resp).Error.Code
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || codigo != "unauthorized" {
			t.Errorf("%s %s sem token deu %d/%s, queria 401 unauthorized", r.metodo, r.caminho, resp.StatusCode, codigo)
		}
	}
}

// As mutações novas exigem X-Requested-With (spec §5.3) e o corpo passa pelo
// limite de 1 MiB, como todas as da API.
func TestPlano5MutationsPassThroughTheChain(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodPost, "/api/v1/auth/password"},
		{http.MethodDelete, "/api/v1/sessions/" + uuid.NewString()},
	} {
		req, err := http.NewRequest(r.metodo, a.srv.URL+r.caminho, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s sem X-Requested-With deu %d, queria 403", r.metodo, r.caminho, resp.StatusCode)
		}
	}

	grande := bytes.Repeat([]byte("a"), 2<<20)
	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/password",
		bytes.NewReader(append(append([]byte(`{"current_password":"`), grande...), []byte(`"}`)...)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Requested-With", "stillon")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("corpo de 2 MiB deu %d, queria 413", resp.StatusCode)
	}
}
