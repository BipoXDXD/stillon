package httpapi_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// nonceDaPagina casa o nonce da CSP, que muda a cada resposta.
var nonceDaPagina = regexp.MustCompile(`nonce="[^"]+"`)

// A página pública não sabe nada de projeto: um PATCH pela rota gêmea que
// regrava os mesmos valores (e esvazia o cache da página) não muda o corpo de
// GET /p/{slug}, a não ser pelo nonce. A página vive num projeto que não é o
// padrão, criada pelas gêmeas: a rota gêmea tem de usar o projeto do caminho.
func TestPublicPageUnchangedAfterProjectScope(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	testdb.ProjetoPadrao(t, a.userID)
	projeto := testdb.NovoProjeto(t, a.userID, "B")
	noProjeto := "/api/v1/projects/" + projeto.String()
	var alvo alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, noProjeto+"/targets", tok, map[string]any{
		"name": "api-interna", "url": "https://interno.exemplo.com/health", "interval_sec": 60,
	}), &alvo)
	var pagina paginaJSON
	resp := chamar(t, a, http.MethodPost, noProjeto+"/status-pages", tok, map[string]any{
		"slug": "empresa", "title": "Status da Empresa", "published": true,
		"items": []map[string]any{{"target_id": alvo.ID, "display_name": "API"}},
	})
	lerCorpo(t, resp, &pagina)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar página pela gêmea deu %d", resp.StatusCode)
	}
	var gravadaEm string
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT project_id::text FROM status_pages WHERE id = $1`, pagina.ID).Scan(&gravadaEm); err != nil {
		t.Fatal(err)
	}
	if gravadaEm != projeto.String() {
		t.Fatalf("a página foi gravada no projeto %s, queria %s", gravadaEm, projeto)
	}

	antes, corpoAntes := getPublico(t, a, "/p/empresa")
	if antes.StatusCode != http.StatusOK {
		t.Fatalf("GET /p/empresa antes deu %d: %s", antes.StatusCode, corpoAntes)
	}

	gemea := noProjeto + "/status-pages/" + pagina.ID
	status, corpo := lerCru(t, a, http.MethodPatch, gemea, tok, map[string]any{
		"title": "Status da Empresa", "published": true,
		"items": []map[string]any{{"target_id": alvo.ID, "display_name": "API"}},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH %s deu %d, queria 200: %s", gemea, status, corpo)
	}
	if lida := decodificar(t, corpo); lida["id"] != pagina.ID || lida["slug"] != "empresa" {
		t.Fatalf("o PATCH pela gêmea devolveu %s, queria a página %s", corpo, pagina.ID)
	}

	depois, corpoDepois := getPublico(t, a, "/p/empresa")
	if depois.StatusCode != http.StatusOK {
		t.Fatalf("GET /p/empresa depois deu %d: %s", depois.StatusCode, corpoDepois)
	}
	semNonce := func(s string) string { return nonceDaPagina.ReplaceAllString(s, `nonce=""`) }
	if semNonce(corpoAntes) != semNonce(corpoDepois) {
		t.Errorf("GET /p/empresa mudou depois do PATCH pela gêmea:\nantes:\n%s\ndepois:\n%s", corpoAntes, corpoDepois)
	}
}
