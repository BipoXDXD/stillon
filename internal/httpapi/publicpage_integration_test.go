package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

func getPublico(t *testing.T, a *api, caminho string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(a.srv.URL + caminho)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

var nonceNoStyle = regexp.MustCompile(`<style nonce="([^"]+)">`)

// Spec §5.6 e §10: a página pública sai sem autenticação, só com o que a
// tabela da §5.6 permite, e com a CSP estrita autorizando o CSS pelo nonce.
func TestPublicPageRendersWithoutLeaking(t *testing.T) {
	a := newAPI(t)
	ctx := context.Background()
	tok := tokenDe(t, a, a.userID)
	if err := storage.EnsureChecksPartitions(ctx, a.pool, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	alvoSecreto := map[string]any{
		"name": "db-primario-interno", "url": "https://interno.exemplo.com/saude-secreta", "interval_sec": 60,
		"headers": map[string]string{"Authorization": "Bearer token-do-cliente"},
	}
	var alvo alvoJSON
	lerCorpo(t, chamar(t, a, http.MethodPost, "/api/v1/targets", tok, alvoSecreto), &alvo)
	kind := "conn_refused"
	err := storage.InProberTx(ctx, a.pool, func(q *db.Queries) error {
		code := int32(503)
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: uuid.MustParse(alvo.ID), CheckedAt: a.clk.Now().Add(-time.Minute),
			Status: "down", LatencyMS: 12, StatusCode: &code, ErrorKind: &kind,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var pagina paginaJSON
	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{
		"slug": "empresa", "title": "Status <da> Empresa", "published": true,
		"items": []map[string]any{{"target_id": alvo.ID, "display_name": "Banco <script>alert(1)</script>"}},
	})
	lerCorpo(t, resp, &pagina)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar página deu %d", resp.StatusCode)
	}

	resp, corpo := getPublico(t, a, "/p/empresa")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("GET /p/empresa deu %d %s: %s", resp.StatusCode, resp.Header.Get("Content-Type"), corpo)
	}
	for _, quer := range []string{"Fora do ar", "Há serviços fora do ar.", "Banco &lt;script&gt;", "Status &lt;da&gt; Empresa"} {
		if !strings.Contains(corpo, quer) {
			t.Errorf("a página não tem %q:\n%s", quer, corpo)
		}
	}
	for _, vazou := range []string{
		"interno.exemplo.com", "saude-secreta", "db-primario-interno", "token-do-cliente", "Authorization",
		alvo.ID, pagina.ID, a.userID.String(), "503", "conn_refused", "<script>",
	} {
		if strings.Contains(corpo, vazou) {
			t.Errorf("a página pública contém %q", vazou)
		}
	}

	csp := resp.Header.Get("Content-Security-Policy")
	m := nonceNoStyle.FindStringSubmatch(corpo)
	if m == nil || !strings.Contains(csp, "style-src 'nonce-"+m[1]+"'") || strings.Contains(csp, "unsafe-inline") ||
		!strings.Contains(csp, "default-src 'none'") || strings.Contains(corpo, "<script") {
		t.Errorf("CSP %q não autoriza só o <style> da página (nonce %v)", csp, m)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=30" {
		t.Errorf("Cache-Control = %q, queria public, max-age=30 (spec §10.1)", cc)
	}

	// O nonce é por resposta.
	_, outro := getPublico(t, a, "/p/empresa")
	if m2 := nonceNoStyle.FindStringSubmatch(outro); m2 == nil || m2[1] == m[1] {
		t.Errorf("o nonce se repetiu entre respostas: %v", m2)
	}
}

// Rascunho e inexistente dão a mesma resposta, byte a byte: o público não
// descobre quais slugs existem. E o 404 não fica em cache.
func TestPublicPage404IsTheSameForDraftAndMissing(t *testing.T) {
	a := newAPI(t)
	tok := tokenDe(t, a, a.userID)
	resp := chamar(t, a, http.MethodPost, "/api/v1/status-pages", tok, map[string]any{"slug": "rascunho", "title": "T"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("criar página deu %d", resp.StatusCode)
	}

	r1, rascunho := getPublico(t, a, "/p/rascunho")
	r2, inexistente := getPublico(t, a, "/p/nao-existe")
	r3, malformado := getPublico(t, a, "/p/NAO_EXISTE")
	for _, r := range []*http.Response{r1, r2, r3} {
		if r.StatusCode != http.StatusNotFound || r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("404 deu %d com Cache-Control %q", r.StatusCode, r.Header.Get("Cache-Control"))
		}
	}
	if rascunho != inexistente || inexistente != malformado {
		t.Errorf("os 404 diferem:\n%s\n%s\n%s", rascunho, inexistente, malformado)
	}
}

// A página é só GET. E ela é pública de verdade: não pede token nem o
// cabeçalho anti-CSRF da API.
func TestPublicPageIsGetOnly(t *testing.T) {
	a := newAPI(t)
	resp, err := http.Post(a.srv.URL+"/p/empresa", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /p/empresa deu %d, queria 405", resp.StatusCode)
	}
}
