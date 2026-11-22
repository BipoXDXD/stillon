package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// As rotas /projects/{projectID}/targets são gêmeas das antigas: no Projeto
// padrão, as duas devolvem o mesmo JSON, e a escrita por uma aparece na
// outra.
func TestProjectTargetRoutesMirrorLegacy(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	noProjeto := "/api/v1/projects/" + testdb.ProjetoPadrao(t, a.userID).String()

	var criado alvoJSON
	resp := chamar(t, a, http.MethodPost, noProjeto+"/targets", token, alvoValido)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST %s/targets deu %d, queria 201: %s", noProjeto, resp.StatusCode, lerCorpo(t, resp, nil))
	}
	lerCorpo(t, resp, &criado)

	mesmoJSON := func(antiga, gemea string) {
		t.Helper()
		sa, ca := lerCru(t, a, http.MethodGet, antiga, token, nil)
		sg, cg := lerCru(t, a, http.MethodGet, gemea, token, nil)
		if sa != http.StatusOK || sg != http.StatusOK {
			t.Fatalf("GET %s deu %d e GET %s deu %d, queria 200 nos dois", antiga, sa, gemea, sg)
		}
		if ca != cg {
			t.Errorf("GET %s e GET %s divergem:\n%s\n%s", antiga, gemea, ca, cg)
		}
	}
	mesmoJSON("/api/v1/targets/"+criado.ID, noProjeto+"/targets/"+criado.ID)
	mesmoJSON("/api/v1/targets", noProjeto+"/targets")
	mesmoJSON("/api/v1/targets/summary", noProjeto+"/targets/summary")
	mesmoJSON("/api/v1/targets/"+criado.ID+"/checks", noProjeto+"/targets/"+criado.ID+"/checks")

	resp = chamar(t, a, http.MethodPatch, noProjeto+"/targets/"+criado.ID, token, map[string]any{"name": "Pela gêmea"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH pela gêmea deu %d, queria 200: %s", resp.StatusCode, lerCorpo(t, resp, nil))
	}
	resp.Body.Close()
	var lido alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, token, nil), &lido)
	if lido.Name != "Pela gêmea" {
		t.Errorf("a rota antiga leu o nome %q depois do PATCH pela gêmea, queria \"Pela gêmea\"", lido.Name)
	}

	resp = chamar(t, a, http.MethodDelete, noProjeto+"/targets/"+criado.ID, token, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE pela gêmea deu %d, queria 204", resp.StatusCode)
	}
	if status, _ := lerCru(t, a, http.MethodGet, "/api/v1/targets/"+criado.ID, token, nil); status != http.StatusNotFound {
		t.Errorf("a rota antiga achou o alvo apagado pela gêmea: %d, queria 404", status)
	}
}

// Mesmo usuário, dois projetos: o alvo de B, pedido pelo caminho de A, é o
// mesmo 404 de um id que não existe.
func TestTargetOfOtherProjectIsNotFoundOnProjectRoutes(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	projetoA := testdb.ProjetoPadrao(t, a.userID)
	projetoB := testdb.NovoProjeto(t, a.userID, "B")

	var deB alvoJSON
	resp := chamar(t, a, http.MethodPost, "/api/v1/projects/"+projetoB.String()+"/targets", token, alvoValido)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST no projeto B deu %d, queria 201: %s", resp.StatusCode, lerCorpo(t, resp, nil))
	}
	lerCorpo(t, resp, &deB)

	emA := "/api/v1/projects/" + projetoA.String() + "/targets/"
	statusB, corpoB := lerCru(t, a, http.MethodGet, emA+deB.ID, token, nil)
	statusX, corpoX := lerCru(t, a, http.MethodGet, emA+uuid.NewString(), token, nil)

	if statusB != http.StatusNotFound || statusX != http.StatusNotFound {
		t.Fatalf("alvo de B pelo caminho de A deu %d e id aleatório deu %d, queria 404 nos dois", statusB, statusX)
	}
	if semTrace(t, corpoB) != semTrace(t, corpoX) {
		t.Errorf("os 404 divergem:\nalvo de B: %s\naleatório: %s", corpoB, corpoX)
	}
	if status, _ := lerCru(t, a, http.MethodGet, "/api/v1/projects/"+projetoB.String()+"/targets/"+deB.ID, token, nil); status != http.StatusOK {
		t.Errorf("o alvo de B pelo caminho de B deu %d, queria 200", status)
	}
}
