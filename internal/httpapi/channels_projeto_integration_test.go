package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O canal de e-mail criado pela rota gêmea, num projeto que não é o padrão,
// vive nesse projeto, e o link de verificação continua confirmando: a rota
// pública /verify/{token} não sabe nada de projeto.
func TestVerifyLinkStillConfirmsProjectChannel(t *testing.T) {
	a := newAPI(t)
	_, sessao := entrar(t, a)
	testdb.ProjetoPadrao(t, a.userID)
	projeto := testdb.NovoProjeto(t, a.userID, "B")
	noProjeto := "/api/v1/projects/" + projeto.String() + "/channels"

	status, corpo := lerCru(t, a, http.MethodPost, noProjeto, sessao,
		map[string]string{"kind": "email", "email": "pessoa@exemplo.com"})
	if status != http.StatusCreated {
		t.Fatalf("POST %s deu %d, queria 201: %s", noProjeto, status, corpo)
	}
	criado := decodificar(t, corpo)
	id, _ := criado["id"].(string)
	if criado["status"] != "pending" {
		t.Fatalf("o canal criado pela gêmea veio com status %v, queria pending: %s", criado["status"], corpo)
	}
	var gravadoEm string
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT project_id::text FROM notif_channels WHERE id = $1`, id).Scan(&gravadoEm); err != nil {
		t.Fatal(err)
	}
	if gravadoEm != projeto.String() {
		t.Fatalf("o canal foi gravado no projeto %s, queria %s", gravadoEm, projeto)
	}

	if rec := confirmarComCorpo(a, a.email.token(t), nil); rec.Code != http.StatusOK {
		t.Fatalf("POST /verify/{token} deu %d, queria 200: %s", rec.Code, rec.Body)
	}

	status, corpo = lerCru(t, a, http.MethodGet, noProjeto, sessao, nil)
	if status != http.StatusOK {
		t.Fatalf("GET pela gêmea deu %d, queria 200: %s", status, corpo)
	}
	itens, _ := decodificar(t, corpo)["items"].([]any)
	if len(itens) != 1 {
		t.Fatalf("GET pela gêmea listou %d canais, queria 1: %s", len(itens), corpo)
	}
	if canal, _ := itens[0].(map[string]any); canal["id"] != id || canal["status"] != "verified" {
		t.Errorf("GET pela gêmea = %v; queria o canal %s verificado", canal, id)
	}
}
