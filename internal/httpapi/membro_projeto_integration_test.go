package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// itensDe devolve o items cru do envelope de uma lista.
func itensDe(t *testing.T, corpo string) string {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(corpo), &envelope); err != nil {
		t.Fatalf("decodificar o envelope %s: %v", corpo, err)
	}
	itens, ok := envelope["items"]
	if !ok {
		t.Fatalf("o envelope não tem items: %s", corpo)
	}
	return string(itens)
}

// O member do Projeto padrão do titular lê pelas rotas de projeto os mesmos
// recursos que o titular criou pelas rotas antigas, e edita o que o papel
// permite: a política é por projeto, não por quem criou a linha.
func TestMemberReadsTitularResourcesOnProjectRoutes(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	r := prepararDono(t, a, dono)
	idDoMember := outroUsuario(t, a)
	testdb.Membro(t, uuid.MustParse(r.projeto), idDoMember, "member")
	member := tokenDe(t, a, idDoMember)

	for _, recurso := range []string{"targets", "heartbeats", "channels", "status-pages"} {
		caminho := "/api/v1/projects/" + r.projeto + "/" + recurso
		esperarVagaNoLimite(a)
		stDono, crDono := lerCru(t, a, http.MethodGet, caminho, dono, nil)
		esperarVagaNoLimite(a)
		stMember, crMember := lerCru(t, a, http.MethodGet, caminho, member, nil)
		if stDono != http.StatusOK || stMember != http.StatusOK {
			t.Errorf("GET %s: dono %d, member %d; queria 200 nos dois:\n%s\n%s", caminho, stDono, stMember, crDono, crMember)
			continue
		}
		doDono := itensDe(t, crDono)
		if doDono == "[]" {
			t.Fatalf("GET %s do dono veio vazio: o teste não prova o que o member vê", caminho)
		}
		if doMember := itensDe(t, crMember); doMember != doDono {
			t.Errorf("GET %s: o member vê outros itens que o dono:\nmember %s\ndono   %s", caminho, doMember, doDono)
		}
	}

	for _, c := range []struct {
		metodo, caminho string
		corpo           any
	}{
		{http.MethodPatch, "/api/v1/projects/" + r.projeto + "/targets/" + r.alvo, map[string]any{"name": "alterado pelo member"}},
		{http.MethodPost, "/api/v1/projects/" + r.projeto + "/incidents/" + r.incidente + "/ack", nil},
	} {
		esperarVagaNoLimite(a)
		if status, corpo := lerCru(t, a, c.metodo, c.caminho, member, c.corpo); status != http.StatusOK {
			t.Errorf("%s %s do member deu %d, queria 200: %s", c.metodo, c.caminho, status, corpo)
		}
	}
}
