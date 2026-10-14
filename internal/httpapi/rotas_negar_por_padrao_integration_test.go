package httpapi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// rotasPublicas é a allowlist explícita do "negar por padrão": as únicas
// rotas registradas que respondem sem access token. Rota nova entra aqui só
// de propósito, com o porquê.
var rotasPublicas = map[string]string{
	"POST /api/v1/auth/login":       "é onde a sessão nasce",
	"POST /api/v1/auth/refresh":     "a credencial é o cookie de refresh, não o access token",
	"POST /api/v1/auth/logout":      "encerra pelo cookie de refresh; sem ele, não há o que encerrar",
	"POST /api/v1/auth/totp/verify": "a credencial é o desafio do login, antes de haver sessão",
	"GET /healthz":                  "liveness do orquestrador",
	"GET /readyz":                   "readiness do orquestrador",
	"GET /verify/{token}":           "a credencial é o token no caminho, aberto pelo e-mail",
	"POST /verify/{token}":          "a credencial é o token no caminho, aberto pelo e-mail",
	"GET /p/{slug}":                 "a página de status é pública por definição",
}

// arquivosDeRotas são os fontes que registram rotas no ServeMux.
var arquivosDeRotas = []string{"routes.go", "publicpage.go"}

// TestEveryRegisteredRouteDeniesByDefault enumera as rotas registradas nos
// fontes (o ServeMux não as expõe) e exige 401 unauthorized sem credencial em
// toda rota fora da allowlist. Uma rota nova esquecida sem Authn, e sem a
// guarda do handler, falha aqui.
func TestEveryRegisteredRouteDeniesByDefault(t *testing.T) {
	rotas := rotasRegistradas(t)
	if len(rotas) < 30 {
		t.Fatalf("achei só %d rotas nos fontes; o parser deixou de enxergar os registros", len(rotas))
	}
	for publica := range rotasPublicas {
		if !slices.Contains(rotas, publica) {
			t.Errorf("a allowlist tem %q, que não está mais registrada: tire-a", publica)
		}
	}

	a := newAPI(t)
	for _, rota := range rotas {
		if _, ok := rotasPublicas[rota]; ok {
			continue
		}
		metodo, caminho, _ := strings.Cut(rota, " ")
		resp := chamar(t, a, metodo, preencherCuringas(caminho), "", nil)
		corpo, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(corpo), `"unauthorized"`) {
			t.Errorf("%s sem credencial deu %d %s, queria 401 unauthorized", rota, resp.StatusCode, corpo)
		}
	}
}

// Com um access token inválido (assinado com outra chave), o resultado é o
// mesmo: a rota não confia em token que não emitiu.
func TestEveryProtectedRouteRejectsForgedToken(t *testing.T) {
	forjado := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJleHAiOjQxMDI0NDQ4MDB9." +
		"c2VtLWFzc2luYXR1cmEtdmFsaWRh"
	a := newAPI(t)
	for _, rota := range rotasRegistradas(t) {
		if _, ok := rotasPublicas[rota]; ok {
			continue
		}
		metodo, caminho, _ := strings.Cut(rota, " ")
		resp := chamar(t, a, metodo, preencherCuringas(caminho), forjado, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s com token forjado deu %d, queria 401", rota, resp.StatusCode)
		}
	}
}

// Toda mutação registrada da API, pública ou não (refresh e logout usam o
// cookie, que é o que um CSRF aproveitaria), exige o X-Requested-With. Só o
// POST /verify/{token} fica de fora: ele vem de um <form> HTML, sem cookie
// nem sessão, e a credencial é o token no caminho.
func TestEveryMutationRequiresTheAntiCSRFHeader(t *testing.T) {
	a := newAPI(t)
	for _, rota := range rotasRegistradas(t) {
		metodo, caminho, _ := strings.Cut(rota, " ")
		if metodo == http.MethodGet || rota == "POST /verify/{token}" {
			continue
		}
		req, err := http.NewRequest(metodo, a.srv.URL+preencherCuringas(caminho), strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", "stillon_refresh=qualquer")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s sem X-Requested-With deu %d, queria 403", rota, resp.StatusCode)
		}
	}
}

// padraoDeRota casa "MÉTODO /caminho", o formato do ServeMux do Go 1.22+.
var padraoDeRota = regexp.MustCompile(`^(GET|HEAD|POST|PUT|PATCH|DELETE|OPTIONS) /\S*$`)

// rotasRegistradas lê os fontes e devolve todo literal "MÉTODO /caminho"
// passado a um .Handle ou .HandleFunc.
func rotasRegistradas(t *testing.T) []string {
	t.Helper()
	var rotas []string
	fset := token.NewFileSet()
	for _, nome := range arquivosDeRotas {
		arquivo, err := parser.ParseFile(fset, nome, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(arquivo, func(n ast.Node) bool {
			if rota, ok := literalDeRegistro(n); ok {
				rotas = append(rotas, rota)
			}
			return true
		})
	}
	return rotas
}

func literalDeRegistro(n ast.Node) (string, bool) {
	chamada, ok := n.(*ast.CallExpr)
	if !ok || len(chamada.Args) == 0 {
		return "", false
	}
	sel, ok := chamada.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
		return "", false
	}
	lit, ok := chamada.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	valor, err := strconv.Unquote(lit.Value)
	if err != nil || !padraoDeRota.MatchString(valor) {
		return "", false
	}
	return valor, true
}

var curinga = regexp.MustCompile(`\{[^}]+\}`)

func preencherCuringas(caminho string) string {
	return curinga.ReplaceAllStringFunc(caminho, func(string) string { return uuid.NewString() })
}
