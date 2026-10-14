package webui_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BipoXDXD/stillon/internal/webui"
)

// indexDeTeste imita o index.html que o Vite gera com html.cspNonce: o
// marcador no <meta> e nos dois elementos que carregam o bundle.
const indexDeTeste = `<!doctype html><html lang="pt-BR"><head>` +
	`<meta property="csp-nonce" nonce="STILLON_CSP_NONCE">` +
	`<script type="module" crossorigin src="/assets/index-abc123.js" nonce="STILLON_CSP_NONCE"></script>` +
	`<link rel="stylesheet" crossorigin href="/assets/index-def456.css" nonce="STILLON_CSP_NONCE">` +
	`</head><body><div id="root"></div></body></html>`

// bundleGrande passa do limite do gzip e comprime bem.
var bundleGrande = strings.Repeat("console.log('stillon');\n", 200)

func distDeTeste() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                      {Data: []byte(indexDeTeste)},
		"assets/index-abc123.js":          {Data: []byte(bundleGrande)},
		"assets/index-def456.css":         {Data: []byte("body{margin:0}")},
		"assets/geist-latin-9f8e7d.woff2": {Data: []byte("wOF2")},
		"assets/estranho-1a2b3c.xyz":      {Data: []byte("?")},
		"fonts/inter-latin.woff2":         {Data: []byte("wOF2")},
		"favicon.svg":                     {Data: []byte("<svg/>")},
		"tema.js":                         {Data: []byte("document.documentElement.classList")},
		".gitkeep":                        {Data: nil},
	}
}

func pedir(t *testing.T, h http.Handler, metodo, caminho string, cab ...string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(metodo, caminho, nil)
	for i := 0; i+1 < len(cab); i += 2 {
		req.Header.Set(cab[i], cab[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func corpo(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var nonceDaCSP = regexp.MustCompile(`style-src 'self' 'nonce-([A-Za-z0-9_-]{22})'`)

// A tabela da spec do Plano 6 §5.2, caso a caso.
func TestRoteamentoDoPainel(t *testing.T) {
	h := webui.New(distDeTeste())
	casos := []struct {
		nome, metodo, caminho string
		status                int
		tipo, cache           string
		index                 bool
	}{
		{"raiz é o index", "GET", "/", 200, "text/html; charset=utf-8", "no-store", true},
		{"rota do painel é o index", "GET", "/targets/0190a1b2-c3d4", 200, "text/html; charset=utf-8", "no-store", true},
		{"rota desconhecida sem extensão também", "GET", "/qualquer/coisa", 200, "text/html; charset=utf-8", "no-store", true},
		{"index.html pelo nome sai com nonce", "GET", "/index.html", 200, "text/html; charset=utf-8", "no-store", true},
		{"asset com hash é imutável", "GET", "/assets/index-abc123.js", 200, "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", false},
		{"css", "GET", "/assets/index-def456.css", 200, "text/css; charset=utf-8", "public, max-age=31536000, immutable", false},
		{"woff2 sem depender do /etc/mime.types", "GET", "/assets/geist-latin-9f8e7d.woff2", 200, "font/woff2", "public, max-age=31536000, immutable", false},
		{"extensão desconhecida não executa", "GET", "/assets/estranho-1a2b3c.xyz", 200, "application/octet-stream", "public, max-age=31536000, immutable", false},
		{"asset ausente é 404, nunca o index", "GET", "/assets/index-velho.js", 404, "text/plain; charset=utf-8", "no-store", false},
		{"arquivo da raiz revalida", "GET", "/favicon.svg", 200, "image/svg+xml", "no-cache", false},
		{"script do tema revalida", "GET", "/tema.js", 200, "text/javascript; charset=utf-8", "no-cache", false},
		{"fonte fora de assets: woff2, revalida (URL estável)", "GET", "/fonts/inter-latin.woff2", 200, "font/woff2", "no-cache", false},
		{"arquivo da raiz ausente é 404", "GET", "/robots.txt", 404, "text/plain; charset=utf-8", "no-store", false},
		{"arquivo oculto não sai", "GET", "/.gitkeep", 404, "text/plain; charset=utf-8", "no-store", false},
		{"diretório de assets não lista", "GET", "/assets/", 200, "text/html; charset=utf-8", "no-store", true},
		{"POST é 405", "POST", "/", 405, "text/plain; charset=utf-8", "no-store", false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			r := pedir(t, h, c.metodo, c.caminho)
			if r.StatusCode != c.status {
				t.Fatalf("status = %d, queria %d", r.StatusCode, c.status)
			}
			if got := r.Header.Get("Content-Type"); got != c.tipo {
				t.Errorf("Content-Type = %q, queria %q", got, c.tipo)
			}
			if got := r.Header.Get("Cache-Control"); got != c.cache {
				t.Errorf("Cache-Control = %q, queria %q", got, c.cache)
			}
			b := corpo(t, r)
			if ehIndex := strings.Contains(b, `<div id="root">`); ehIndex != c.index {
				t.Errorf("corpo é o index = %v, queria %v", ehIndex, c.index)
			}
			if strings.Contains(b, webui.Marcador) {
				t.Errorf("o marcador saiu no corpo: %q", b)
			}
		})
	}
	if r := pedir(t, h, "POST", "/"); r.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("405 sem Allow: %q", r.Header.Get("Allow"))
	}
}

// O nonce do cabeçalho é o do <meta> e o dos dois elementos do bundle. Se um
// deles divergisse, o navegador bloquearia justamente os <style> que o
// painel injeta, e o gráfico sumiria (spec do Plano 6 §8.1).
func TestIndexLevaONonceDaCSP(t *testing.T) {
	h := webui.New(distDeTeste())
	r := pedir(t, h, "GET", "/")
	csp := r.Header.Get("Content-Security-Policy")
	m := nonceDaCSP.FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("CSP sem nonce de estilo: %q", csp)
	}
	b := corpo(t, r)
	if n := strings.Count(b, `nonce="`+m[1]+`"`); n != 3 {
		t.Errorf("o nonce da CSP aparece %d vezes no corpo, queria 3 (meta, script, link):\n%s", n, b)
	}
	for _, diretiva := range []string{"default-src 'none'", "script-src 'self';", "frame-ancestors 'none'",
		"base-uri 'none'", "form-action 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, diretiva) {
			t.Errorf("CSP sem %q: %s", diretiva, csp)
		}
	}
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP afrouxada: %s", csp)
	}
}

// Um nonce repetido deixa de ser segredo da resposta. Sabotagem prevista:
// sortear o nonce uma vez, em New.
func TestCadaRespostaTemNonceNovo(t *testing.T) {
	h := webui.New(distDeTeste())
	vistos := map[string]bool{}
	for range 50 {
		m := nonceDaCSP.FindStringSubmatch(pedir(t, h, "GET", "/").Header.Get("Content-Security-Policy"))
		if m == nil {
			t.Fatal("CSP sem nonce")
		}
		if vistos[m[1]] {
			t.Fatalf("nonce repetido entre respostas: %s", m[1])
		}
		vistos[m[1]] = true
	}
}

func TestHeadNaoTemCorpo(t *testing.T) {
	h := webui.New(distDeTeste())
	for _, c := range []string{"/", "/assets/index-abc123.js"} {
		r := pedir(t, h, "HEAD", c)
		if r.StatusCode != 200 || corpo(t, r) != "" || r.Header.Get("Content-Length") == "" {
			t.Errorf("HEAD %s: status %d, Content-Length %q", c, r.StatusCode, r.Header.Get("Content-Length"))
		}
	}
}

func TestGzipQuandoOClienteAceita(t *testing.T) {
	h := webui.New(distDeTeste())
	r := pedir(t, h, "GET", "/assets/index-abc123.js", "Accept-Encoding", "br, gzip")
	if r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("sem gzip: Content-Encoding %q, Vary %q", r.Header.Get("Content-Encoding"), r.Header.Get("Vary"))
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil || string(b) != bundleGrande {
		t.Fatalf("gzip não devolve o arquivo (%v)", err)
	}

	for _, ae := range []string{"", "br", "gzip;q=0", "identity"} {
		r := pedir(t, h, "GET", "/assets/index-abc123.js", "Accept-Encoding", ae)
		if r.Header.Get("Content-Encoding") != "" || corpo(t, r) != bundleGrande {
			t.Errorf("Accept-Encoding %q recebeu gzip", ae)
		}
	}
	// Arquivo pequeno não compensa: sai cru mesmo com gzip aceito.
	r = pedir(t, h, "GET", "/assets/index-def456.css", "Accept-Encoding", "gzip")
	if r.Header.Get("Content-Encoding") != "" {
		t.Error("arquivo de 14 bytes saiu em gzip")
	}
}

// go build sem npm run build: o dist só tem o .gitkeep. O painel responde
// 503 dizendo o que fazer, e os assets continuam 404 (e não 503: não há
// asset nenhum).
func TestSemBuildResponde503(t *testing.T) {
	h := webui.New(fstest.MapFS{".gitkeep": {}})
	r := pedir(t, h, "GET", "/targets")
	if r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(corpo(t, r), "npm run build") {
		t.Fatalf("status %d", r.StatusCode)
	}
	if r := pedir(t, h, "GET", "/assets/index-abc123.js"); r.StatusCode != http.StatusNotFound {
		t.Errorf("asset sem build: status %d, queria 404", r.StatusCode)
	}
}

// Um build feito sem html.cspNonce produziria um painel cujo gráfico some.
// Melhor 503 explícito que isso.
func TestIndexSemMarcadorResponde503(t *testing.T) {
	d := distDeTeste()
	d["index.html"] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(indexDeTeste, webui.Marcador, "fixo"))}
	r := pedir(t, webui.New(d), "GET", "/")
	if r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(corpo(t, r), "marcador") {
		t.Fatalf("status %d", r.StatusCode)
	}
}

// O dist embutido de verdade: com só o .gitkeep (go test sem build) ou com
// um build completo, todo arquivo dele tem de ter tipo na tabela. É a guarda
// que o teste de .woff2 acima não prova sozinho: numa máquina com
// /etc/mime.types, trocar a tabela por mime.TypeByExtension continuaria
// verde lá, e ficaria vermelho aqui assim que o build tivesse uma fonte.
func TestTodoArquivoDoDistTemTipo(t *testing.T) {
	dist := webui.Dist()
	n := 0
	err := fs.WalkDir(dist, ".", func(nome string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(nome, ".") {
			return err
		}
		n++
		if webui.TipoDe(nome) == "application/octet-stream" {
			t.Errorf("%s sem tipo na tabela do painel", nome)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		// Sem build, o teste não confere nada. Quem liga
		// STILLON_EXIGIR_DIST=1 é o CI (ci.yml, job web, no passo depois do
		// "npm run build"; Plano 6), e não o build: lá, um dist vazio é
		// erro, e não um verde vazio.
		if os.Getenv("STILLON_EXIGIR_DIST") == "1" {
			t.Fatal("STILLON_EXIGIR_DIST=1 e o dist embutido está vazio: rode npm run build em web/ antes")
		}
		t.Log("dist sem build (só o .gitkeep): nada a conferir")
		return
	}
	idx, err := fs.ReadFile(dist, "index.html")
	if err != nil || !bytes.Contains(idx, []byte(webui.Marcador)) {
		t.Errorf("o dist embutido não tem index.html com o marcador (%v)", err)
	}
}
