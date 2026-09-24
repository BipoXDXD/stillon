package httpapi_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/apierr"
)

type paginaJSON struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Published bool   `json:"published"`
	Items     []struct {
		TargetID    string `json:"target_id"`
		DisplayName string `json:"display_name"`
	} `json:"items"`
	PublicURL string `json:"public_url"`
}

func paginasDe(t *testing.T, a *api, tok string) []paginaJSON {
	t.Helper()
	var l struct {
		Items []paginaJSON `json:"items"`
	}
	resp := chamar(t, a, http.MethodGet, "/api/v1/status-pages", tok, nil)
	cru := lerCorpo(t, resp, &l)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(cru), `"items":[`) {
		t.Fatalf("GET /status-pages deu %d: %s", resp.StatusCode, cru)
	}
	return l.Items
}

func TestStatusPageFlow(t *testing.T) {
	a := newAPIWith(t, nil)
	tok := tokenDe(t, a, a.userID)
	if ps := paginasDe(t, a, tok); len(ps) != 0 {
		t.Fatalf("antes de criar: %+v", ps)
	}
	var alvo alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &alvo)

	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "empresa", "title": "Status da Empresa",
		"items": []map[string]any{{"target_id": alvo.ID, "display_name": "API pública"}},
	})
	var criada paginaJSON
	cru := lerCorpo(t, resp, &criada)
	if resp.StatusCode != http.StatusCreated || criada.Slug != "empresa" || criada.Published ||
		len(criada.Items) != 1 || criada.Items[0].TargetID != alvo.ID || criada.PublicURL != "/p/empresa" {
		t.Fatalf("POST deu %d: %s", resp.StatusCode, cru)
	}
	if ps := paginasDe(t, a, tok); len(ps) != 1 || ps[0].ID != criada.ID {
		t.Fatalf("lista = %+v", ps)
	}

	resp = chamar(t, a, http.MethodPatch, "/api/v1/status-pages/"+criada.ID, tok, map[string]any{"published": true})
	var mudada paginaJSON
	cru = lerCorpo(t, resp, &mudada)
	if resp.StatusCode != http.StatusOK || !mudada.Published || mudada.Title != "Status da Empresa" || len(mudada.Items) != 1 {
		t.Errorf("PATCH deu %d: %s", resp.StatusCode, cru)
	}

	// Segunda página: 409 com o código próprio.
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{"slug": "outra", "title": "T"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusConflict || env.Error.Code != "status_page_exists" {
		t.Errorf("segunda página deu %d/%s, queria 409 status_page_exists", resp.StatusCode, env.Error.Code)
	}

	// Slug de outro usuário: 409 slug_taken.
	tokB := tokenDe(t, a, outroUsuario(t, a))
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tokB, map[string]any{"slug": "empresa", "title": "T"})
	lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusConflict || env.Error.Code != "slug_taken" {
		t.Errorf("slug tomado deu %d/%s, queria 409 slug_taken", resp.StatusCode, env.Error.Code)
	}

	// Outro usuário não vê nem muda a página de A.
	if ps := paginasDe(t, a, tokB); len(ps) != 0 {
		t.Errorf("B vê a página de A: %+v", ps)
	}
	resp = chamar(t, a, http.MethodPatch, "/api/v1/status-pages/"+criada.ID, tokB, map[string]any{"title": "invadido"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("PATCH de B na página de A deu %d, queria 404", resp.StatusCode)
	}
}

func TestStatusPageValidationPointsAtTheItem(t *testing.T) {
	a := newAPIWith(t, nil)
	tok := tokenDe(t, a, a.userID)
	var alvo alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoValido), &alvo)

	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "Empresa", "title": "",
		"items": []map[string]any{
			{"target_id": alvo.ID, "display_name": "ok"},
			{"target_id": "01a0d30e-3f44-7bb1-a0f2-6d0430af7fd3", "display_name": "<script>"},
		},
	})
	var env envelopeComCampos
	cru := lerCorpo(t, resp, &env)
	quer := []apierr.FieldError{
		{Field: "slug", Reason: "invalid"}, {Field: "title", Reason: "required"},
	}
	if resp.StatusCode != http.StatusBadRequest || !slices.Equal(env.Error.Fields, quer) {
		t.Errorf("POST inválido deu %d: %s; queria %+v", resp.StatusCode, cru, quer)
	}
	if strings.Contains(string(cru), "script") || strings.Contains(string(cru), "Empresa") {
		t.Errorf("o 400 ecoou a entrada: %s", cru)
	}

	// Com os campos da página certos, o alvo alheio aparece pela posição.
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "empresa", "title": "T",
		"items": []map[string]any{
			{"target_id": alvo.ID, "display_name": "ok"},
			{"target_id": "01a0d30e-3f44-7bb1-a0f2-6d0430af7fd3", "display_name": "x"},
		},
	})
	cru = lerCorpo(t, resp, &env)
	if resp.StatusCode != http.StatusBadRequest || len(env.Error.Fields) != 1 ||
		env.Error.Fields[0] != (apierr.FieldError{Field: "items[1].target_id", Reason: "invalid"}) {
		t.Errorf("alvo inexistente deu %d: %s", resp.StatusCode, cru)
	}

	// Campo desconhecido é 400: um "publised" ignorado em silêncio faria o
	// dono achar que publicou.
	resp = chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{"slug": "empresa", "title": "T", "publised": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("campo desconhecido deu %d, queria 400", resp.StatusCode)
	}
}

// Prova a propriedade (401 sem token), não o middleware: o handler também
// responde 401 quando não acha o usuário no contexto. Quem pega a rota sem
// authn é TestStatusPageFlow, em que o dono passa a receber 401.
func TestStatusPageRoutesRequireAuthentication(t *testing.T) {
	a := newAPIWith(t, nil)
	for _, r := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/status-pages"},
		{http.MethodPost, "/api/v1/status-pages"},
		{http.MethodPatch, "/api/v1/status-pages/01a0d30e-3f44-7bb1-a0f2-6d0430af7fd3"},
	} {
		resp := chamar(t, a, r.metodo, r.caminho, "", map[string]any{})
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s sem token deu %d, queria 401", r.metodo, r.caminho, resp.StatusCode)
		}
	}
}
