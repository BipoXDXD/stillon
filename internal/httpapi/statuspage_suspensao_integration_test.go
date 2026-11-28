package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Com o titular suspenso, a página publicada some do público com o mesmo 404
// de um slug que nunca existiu: ninguém de fora distingue "suspenso" de
// "inexistente". A página não é apagada nem despublicada.
func TestSuspendedTitularPublicPageIsNotFound(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "suspensa", "title": "Status", "published": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar página deu %d", resp.StatusCode)
	}
	testdb.Suspender(t, a.userID)

	r1, suspensa := getPublico(t, a, "/p/suspensa")
	r2, inexistente := getPublico(t, a, "/p/nao-existe")
	for _, r := range []*http.Response{r1, r2} {
		if r.StatusCode != http.StatusNotFound || r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s deu %d com Cache-Control %q; queria 404 no-store",
				r.Request.URL.Path, r.StatusCode, r.Header.Get("Cache-Control"))
		}
	}
	if suspensa != inexistente {
		t.Errorf("o 404 da página suspensa difere do inexistente:\n%s\n%s", suspensa, inexistente)
	}
}
