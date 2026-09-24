package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BipoXDXD/vigilume/internal/config"
	"github.com/BipoXDXD/vigilume/internal/httpapi"
)

// indexFalso é o bastante para reconhecer o painel numa resposta.
const indexFalso = `<!doctype html><meta property="csp-nonce" nonce="VIGILUME_CSP_NONCE">` +
	`<div id="root"></div>`

func comPainel(d *httpapi.Deps) {
	d.Painel = fstest.MapFS{
		"index.html":          {Data: []byte(indexFalso)},
		"assets/index-a1b.js": {Data: []byte("export {}")},
	}
}

// O wiring, e não a peça: cada prefixo cai na cadeia dele, pelo Routes()
// inteiro. Os testes de middleware do Plano 1 provaram as peças e deixaram
// o arquivo de rotas sem trava (lição de teste do projeto); este teste
// existe para que tirar uma linha da raiz fique vermelho.
func TestCadaPrefixoCaiNaCadeiaCerta(t *testing.T) {
	a := newAPICom(t, func(c *config.Config) { c.PublicBaseURL = "https://vigilume.exemplo.com" }, comPainel)

	casos := []struct {
		caminho string
		status  int
		painel  bool
		tipo    string
	}{
		// A API continua respondendo como API, inclusive onde não há rota:
		// um /api/ que caísse no painel devolveria o index com 200.
		{"/api/v1/nao-existe", http.StatusNotFound, false, "text/plain; charset=utf-8"},
		{"/api/v1/me", http.StatusUnauthorized, false, "application/json; charset=utf-8"},
		{"/healthz", http.StatusOK, false, ""},
		{"/readyz", http.StatusOK, false, ""},
		// A página pública e a verificação têm as páginas delas.
		{"/p/slug-que-nao-existe", http.StatusNotFound, false, "text/html; charset=utf-8"},
		{"/verify/token-que-nao-existe", http.StatusNotFound, false, "text/html; charset=utf-8"},
		// O resto é o painel.
		{"/", http.StatusOK, true, "text/html; charset=utf-8"},
		{"/targets/0190a1b2", http.StatusOK, true, "text/html; charset=utf-8"},
		{"/assets/index-a1b.js", http.StatusOK, false, "text/javascript; charset=utf-8"},
	}
	for _, c := range casos {
		t.Run(c.caminho, func(t *testing.T) {
			resp, err := http.Get(a.srv.URL + c.caminho)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != c.status {
				t.Fatalf("status = %d, queria %d (%s)", resp.StatusCode, c.status, b)
			}
			if c.tipo != "" && resp.Header.Get("Content-Type") != c.tipo {
				t.Errorf("Content-Type = %q, queria %q", resp.Header.Get("Content-Type"), c.tipo)
			}
			if ehPainel := strings.Contains(string(b), `<div id="root">`); ehPainel != c.painel {
				t.Errorf("é o painel = %v, queria %v", ehPainel, c.painel)
			}
			if c.painel {
				csp := resp.Header.Get("Content-Security-Policy")
				if !strings.Contains(csp, "style-src 'self' 'nonce-") {
					t.Errorf("o painel saiu sem a CSP dele: %q", csp)
				}
				// SecurityHeaders também está na cadeia do painel.
				if resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Strict-Transport-Security") == "" {
					t.Error("o painel saiu sem os cabeçalhos de segurança")
				}
			}
		})
	}
}

// A cadeia do painel não tem CORS nem exige X-Requested-With: é GET de
// arquivo. E a API continua exigindo o cabeçalho nas mutações, agora
// montada em /api/.
func TestAMutacaoDaAPIContinuaExigindoOCabecalho(t *testing.T) {
	a := newAPICom(t, nil, comPainel)
	resp, err := http.Post(a.srv.URL+"/api/v1/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST sem X-Requested-With: status %d, queria 403", resp.StatusCode)
	}
}
