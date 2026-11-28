package httpapi_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

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

// Slug reservado responde como ocupado, e nada é gravado.
func TestCreateWithReservedSlugIsTaken(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	noProjeto := "/api/v1/projects/" + projeto.String() + "/status-pages"
	status, corpo := lerCru(t, a, http.MethodPost, noProjeto, tok, map[string]any{"slug": "admin", "title": "T"})
	if status != http.StatusConflict || codigoErro(t, corpo) != "slug_taken" {
		t.Fatalf("POST com slug reservado deu %d: %s; queria 409 slug_taken", status, corpo)
	}
	status, corpo = lerCru(t, a, http.MethodGet, noProjeto, tok, nil)
	if status != http.StatusOK || !strings.Contains(corpo, `"items":[]`) {
		t.Fatalf("depois do 409, GET %s deu %d: %s; queria a lista vazia", noProjeto, status, corpo)
	}
}

// Trocar para um slug reservado também é 409, e o slug antigo fica.
func TestRenameToReservedSlugIsTaken(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	noProjeto := "/api/v1/projects/" + projeto.String() + "/status-pages"
	var pagina paginaJSON
	resp := chamar(t, a, http.MethodPost, noProjeto, tok, map[string]any{"slug": "minha-loja", "title": "T"})
	lerCorpo(t, resp, &pagina)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar a página deu %d", resp.StatusCode)
	}
	status, corpo := lerCru(t, a, http.MethodPatch, noProjeto+"/"+pagina.ID, tok, map[string]any{"slug": "api"})
	if status != http.StatusConflict || codigoErro(t, corpo) != "slug_taken" {
		t.Fatalf("PATCH para slug reservado deu %d: %s; queria 409 slug_taken", status, corpo)
	}
	status, corpo = lerCru(t, a, http.MethodGet, noProjeto, tok, nil)
	if status != http.StatusOK || !strings.Contains(corpo, `"slug":"minha-loja"`) {
		t.Fatalf("depois do 409, GET %s deu %d: %s; queria minha-loja", noProjeto, status, corpo)
	}
}

// A página que já tinha um slug hoje reservado fica com ele: mudar o título ou
// regravar o mesmo slug continua valendo, e a página pública segue no ar.
func TestExistingReservedSlugKeepsWorking(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testdb.Admin(t).Exec(context.Background(),
		`INSERT INTO status_pages (id, user_id, project_id, slug, title, published, created_at)
		 VALUES ($1, $2, $3, 'status', 'Antiga', true, now())`, id, a.userID, projeto); err != nil {
		t.Fatal(err)
	}
	rota := "/api/v1/projects/" + projeto.String() + "/status-pages/" + id.String()
	for _, patch := range []map[string]any{{"title": "Nova"}, {"slug": "status"}} {
		status, corpo := lerCru(t, a, http.MethodPatch, rota, tok, patch)
		if status != http.StatusOK {
			t.Fatalf("PATCH %v na página antiga deu %d: %s; queria 200", patch, status, corpo)
		}
	}
	if resp, corpo := getPublico(t, a, "/p/status"); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /p/status deu %d: %s", resp.StatusCode, corpo)
	}
}

func codigoErro(t *testing.T, corpo string) string {
	t.Helper()
	codigo, _ := erroDe(t, corpo)
	return codigo
}

// Quem não pode gerir a status page do projeto recebe a mesma recusa para slug
// reservado e para slug livre: o 409 só pode aparecer depois da permissão,
// senão revelaria a lista. Vale para o de fora (404, barrado antes do
// service) e para o member (403 do Require), no POST e no PATCH.
func TestNoPermissionGetsSameRefusalForReservedSlug(t *testing.T) {
	a := newAPI(t)
	dono := tokenDe(t, a, a.userID)
	projeto := testdb.ProjetoPadrao(t, a.userID)
	rota := "/api/v1/projects/" + projeto.String() + "/status-pages"
	var pagina paginaJSON
	resp := chamar(t, a, http.MethodPost, rota, dono, map[string]any{"slug": "minha-loja", "title": "T"})
	lerCorpo(t, resp, &pagina)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar a página do dono deu %d", resp.StatusCode)
	}
	idDoMember := outroUsuario(t, a)
	testdb.Membro(t, projeto, idDoMember, "member")
	quem := []struct {
		nome   string
		token  string
		status int
	}{
		{"de fora", tokenDe(t, a, outroUsuario(t, a)), http.StatusNotFound},
		{"member", tokenDe(t, a, idDoMember), http.StatusForbidden},
	}
	for _, q := range quem {
		for _, op := range []struct{ metodo, caminho string }{
			{http.MethodPost, rota},
			{http.MethodPatch, rota + "/" + pagina.ID},
		} {
			stLivre, corpoLivre := lerCru(t, a, op.metodo, op.caminho, q.token, map[string]any{"slug": "slug-livre", "title": "T"})
			stRes, corpoRes := lerCru(t, a, op.metodo, op.caminho, q.token, map[string]any{"slug": "admin", "title": "T"})
			if stLivre != q.status || stRes != stLivre || codigoErro(t, corpoRes) != codigoErro(t, corpoLivre) {
				t.Errorf("%s %s do %s: slug livre deu %d %s, reservado deu %d %s; queria a mesma recusa %d",
					op.metodo, op.caminho, q.nome, stLivre, corpoLivre, stRes, corpoRes, q.status)
			}
		}
	}
}
