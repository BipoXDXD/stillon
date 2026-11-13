package httpapi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"path/filepath"
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
	"POST /api/v1/auth/login":        "é onde a sessão nasce",
	"POST /api/v1/auth/refresh":      "a credencial é o cookie de refresh, não o access token",
	"POST /api/v1/auth/logout":       "encerra pelo cookie de refresh; sem ele, não há o que encerrar",
	"POST /api/v1/auth/totp/verify":  "a credencial é o desafio do login, antes de haver sessão",
	"GET /healthz":                   "liveness do orquestrador",
	"GET /readyz":                    "readiness do orquestrador",
	"GET /verify/{token}":            "a credencial é o token no caminho, aberto pelo e-mail",
	"POST /verify/{token}":           "a credencial é o token no caminho, aberto pelo e-mail",
	"GET /p/{slug}":                  "a página de status é pública por definição",
	"POST /ping/{token}":             "a credencial é o token no caminho, enviado por uma tarefa agendada",
	"POST /ping/{token}/start":       "a credencial é o token no caminho, enviado por uma tarefa agendada",
	"POST /ping/{token}/fail":        "a credencial é o token no caminho, enviado por uma tarefa agendada",
	"POST /ping/{token}/log":         "a credencial é o token no caminho, enviado por uma tarefa agendada",
	"POST /ping/{token}/{exit_code}": "a credencial é o token no caminho, enviado por uma tarefa agendada",
}

// montagens são os registros sem método da raiz de Routes: cada um entrega o
// prefixo a uma cadeia cujas rotas, com método, a enumeração já cobre. "/" é
// o painel, estático e público. Montagem nova entra aqui só de propósito.
var montagens = []string{"/", "/api/", "/healthz", "/p/", "/ping/", "/readyz", "/verify/"}

// TestEveryRegisteredRouteDeniesByDefault enumera as rotas registradas nos
// fontes (o ServeMux não as expõe) e exige 401 unauthorized sem credencial em
// toda rota fora da allowlist. Uma rota nova esquecida sem Authn, e sem a
// guarda do handler, falha aqui.
func TestEveryRegisteredRouteDeniesByDefault(t *testing.T) {
	rotas := rotasRegistradas(t)
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
		esperarVagaNoLimite(a)
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
		esperarVagaNoLimite(a)
		metodo, caminho, _ := strings.Cut(rota, " ")
		resp := chamar(t, a, metodo, preencherCuringas(caminho), forjado, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s com token forjado deu %d, queria 401", rota, resp.StatusCode)
		}
	}
}

// esperarVagaNoLimite devolve um token aos limites por IP da API: o global
// (120/min, rajada de 30) e o do login (10/min, rajada de 5). A enumeração sai
// toda do mesmo IP, e com o relógio parado passaria da rajada: o 429 esconderia
// o 401 que o teste quer ver. Uma ficha do mais lento (umaFichaPorIP) basta para
// os dois. As rotas autenticadas têm o balde por conta, que vem depois de authn:
// com token forjado, nunca é gasto.
func esperarVagaNoLimite(a *api) { esperarFichaDoIP(a) }

// Toda mutação registrada da API, pública ou não (refresh e logout usam o
// cookie, que é o que um CSRF aproveitaria), exige o X-Requested-With. Só o
// POST /verify/{token} e o /ping/ ficam de fora: vêm de um <form> HTML e de um
// curl, sem cookie nem sessão, e a credencial é o token no caminho.
func TestEveryMutationRequiresTheAntiCSRFHeader(t *testing.T) {
	a := newAPI(t)
	for _, rota := range rotasRegistradas(t) {
		metodo, caminho, _ := strings.Cut(rota, " ")
		if metodo == http.MethodGet || rota == "POST /verify/{token}" || strings.HasPrefix(rota, "POST /ping/") {
			continue
		}
		esperarVagaNoLimite(a)
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

// rotasRegistradas lê todos os fontes do pacote (fora os de teste) e devolve
// todo literal "MÉTODO /caminho" passado a um .Handle ou .HandleFunc. Falha
// alto no que não sabe classificar, para que um registro novo nunca fique de
// fora em silêncio: padrão que não é literal (constante, Sprintf) e montagem
// sem método fora da lista de montagens.
func rotasRegistradas(t *testing.T) []string {
	t.Helper()
	fontes, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var rotas, achadas []string
	fset := token.NewFileSet()
	for _, nome := range fontes {
		if strings.HasSuffix(nome, "_test.go") {
			continue
		}
		arquivo, err := parser.ParseFile(fset, nome, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(arquivo, func(n ast.Node) bool {
			padrao, ehRegistro := padraoDeRegistro(n)
			switch {
			case !ehRegistro:
			case padrao == "":
				t.Errorf("%s: registro de rota com padrão que não é literal; escreva o literal para a enumeração enxergá-lo",
					fset.Position(n.Pos()))
			case padraoDeRota.MatchString(padrao):
				rotas = append(rotas, padrao)
			default:
				achadas = append(achadas, padrao)
			}
			return true
		})
	}
	slices.Sort(achadas)
	if !slices.Equal(achadas, montagens) {
		t.Errorf("montagens sem método = %q, queria %q: rota sem método escapa da enumeração", achadas, montagens)
	}
	if len(rotas) < 30 {
		t.Fatalf("achei só %d rotas nos fontes; o parser deixou de enxergar os registros", len(rotas))
	}
	return rotas
}

// padraoDeRegistro diz se n é uma chamada .Handle ou .HandleFunc e devolve o
// padrão quando ele é um literal de string, ou "" quando não é.
func padraoDeRegistro(n ast.Node) (string, bool) {
	chamada, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := chamada.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
		return "", false
	}
	if len(chamada.Args) == 0 {
		return "", true
	}
	lit, ok := chamada.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", true
	}
	valor, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", true
	}
	return valor, true
}

var curinga = regexp.MustCompile(`\{[^}]+\}`)

func preencherCuringas(caminho string) string {
	return curinga.ReplaceAllStringFunc(caminho, func(string) string { return uuid.NewString() })
}
