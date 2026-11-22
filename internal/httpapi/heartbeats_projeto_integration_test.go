package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O heartbeat criado pela rota gêmea, num projeto que não é o padrão, vive
// nesse projeto, e o token dele continua valendo na rota pública de ping, que
// não sabe nada de projeto.
func TestPingTokenStillWorksAfterProjectMove(t *testing.T) {
	a := newAPI(t)
	_, token := entrar(t, a)
	testdb.ProjetoPadrao(t, a.userID)
	projeto := testdb.NovoProjeto(t, a.userID, "B")
	noProjeto := "/api/v1/projects/" + projeto.String() + "/heartbeats"

	status, corpo := lerCru(t, a, http.MethodPost, noProjeto, token, corpoDePeriodo)
	if status != http.StatusCreated {
		t.Fatalf("POST %s deu %d, queria 201: %s", noProjeto, status, corpo)
	}
	criado := decodificar(t, corpo)
	id := criado["id"].(string)
	var gravadoEm string
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT project_id::text FROM heartbeats WHERE id = $1`, id).Scan(&gravadoEm); err != nil {
		t.Fatal(err)
	}
	if gravadoEm != projeto.String() {
		t.Fatalf("o heartbeat foi gravado no projeto %s, queria %s", gravadoEm, projeto)
	}
	url, _ := criado["ping_url"].(string)
	_, ping, ok := strings.Cut(url, "/ping/")
	if !ok {
		t.Fatalf("ping_url %q", url)
	}

	if status, _, corpo := pingar(t, a, "/ping/"+ping, nil); status != http.StatusOK {
		t.Fatalf("POST /ping/{token} deu %d, queria 200: %s", status, corpo)
	}

	status, corpo = lerCru(t, a, http.MethodGet, noProjeto+"/"+id, token, nil)
	if status != http.StatusOK {
		t.Fatalf("GET pela gêmea deu %d, queria 200: %s", status, corpo)
	}
	if lido := decodificar(t, corpo); lido["last_ping_at"] == nil {
		t.Errorf("o GET pela gêmea não viu o ping: %s", corpo)
	}
}
