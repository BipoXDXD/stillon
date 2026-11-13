package httpapi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// raizDoBaldeDaConta é a variável de routes.go que guarda o limite por conta
// (10/min, rajada 5) das rotas autenticadas do 2FA e da troca de senha.
const raizDoBaldeDaConta = "limiteConta"

// A lista fixa rotasDaConta alimenta os testes do balde por conta; o router é
// a fonte da verdade. Uma rota nova que use o balde, e não entre na lista,
// ficaria fora desses testes sem ninguém notar.
func TestAccountBucketRouteListMatchesTheRouter(t *testing.T) {
	doRouter := rotasComBaldeDaConta(t)
	daLista := slices.Sorted(slices.Values(rotasDaConta))
	if !slices.Equal(doRouter, daLista) {
		t.Fatalf("rotas com o balde por conta no router = %q, rotasDaConta = %q: acrescente a rota nova à lista", doRouter, daLista)
	}
}

// rotasComBaldeDaConta lê os fontes do pacote (fora os de teste) e devolve, em
// ordem, o caminho de toda rota cujo handler passa pelo balde por conta: direto
// por raizDoBaldeDaConta ou por qualquer variável montada a partir dela (hoje,
// autenticadaComLimite). Falha alto se a raiz sumir ou se uma rota com o balde
// não for POST, porque o teste só sabe mandar POST.
func rotasComBaldeDaConta(t *testing.T) []string {
	t.Helper()
	fontes, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var rotas []string
	raizAchada := false
	fset := token.NewFileSet()
	for _, nome := range fontes {
		if strings.HasSuffix(nome, "_test.go") {
			continue
		}
		arquivo, err := parser.ParseFile(fset, nome, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		comBalde := nomesDerivadosDe(arquivo, raizDoBaldeDaConta)
		if declaraVariavel(arquivo, raizDoBaldeDaConta) {
			raizAchada = true
		}
		ast.Inspect(arquivo, func(n ast.Node) bool {
			padrao, ehRegistro := padraoDeRegistro(n)
			if !ehRegistro || padrao == "" {
				return true
			}
			chamada := n.(*ast.CallExpr)
			if len(chamada.Args) < 2 || !citaAlgumNome(chamada.Args[1], comBalde) {
				return true
			}
			metodo, caminho, ok := strings.Cut(padrao, " ")
			if !ok || metodo != http.MethodPost {
				t.Errorf("%s: rota %q usa o balde por conta e não é POST; ensine o teste a mandar o método dela",
					fset.Position(n.Pos()), padrao)
				return true
			}
			rotas = append(rotas, caminho)
			return true
		})
	}
	if !raizAchada {
		t.Fatalf("nenhum fonte declara %q; se ela foi renomeada, atualize raizDoBaldeDaConta", raizDoBaldeDaConta)
	}
	if len(rotas) == 0 {
		t.Fatalf("nenhuma rota registrada passa por %q; o parser deixou de enxergar os registros", raizDoBaldeDaConta)
	}
	slices.Sort(rotas)
	return rotas
}

// nomesDerivadosDe devolve raiz mais todo nome atribuído a partir de uma
// expressão que cita algum nome do conjunto, até o ponto fixo. Segue `x := ...`,
// `x = ...` e `var x = ...`; não segue o limitador passado como argumento a uma
// função auxiliar nem derivação entre arquivos.
func nomesDerivadosDe(arquivo *ast.File, raiz string) map[string]bool {
	nomes := map[string]bool{raiz: true}
	for cresceu := true; cresceu; {
		cresceu = false
		ast.Inspect(arquivo, func(n ast.Node) bool {
			destinos, valores, ok := atribuicaoDe(n)
			if !ok || !slices.ContainsFunc(valores, func(e ast.Expr) bool { return citaAlgumNome(e, nomes) }) {
				return true
			}
			for _, id := range destinos {
				if !nomes[id.Name] {
					nomes[id.Name] = true
					cresceu = true
				}
			}
			return true
		})
	}
	return nomes
}

// declaraVariavel diz se o arquivo atribui um valor a nome, por `:=`, `=` ou `var`.
func declaraVariavel(arquivo *ast.File, nome string) bool {
	achou := false
	ast.Inspect(arquivo, func(n ast.Node) bool {
		destinos, _, _ := atribuicaoDe(n)
		if slices.ContainsFunc(destinos, func(id *ast.Ident) bool { return id.Name == nome }) {
			achou = true
		}
		return !achou
	})
	return achou
}

// atribuicaoDe devolve os identificadores do lado esquerdo e as expressões do
// lado direito de um `x := ...`, `x = ...` ou `var x = ...`; ok é falso para
// qualquer outro nó.
func atribuicaoDe(n ast.Node) (destinos []*ast.Ident, valores []ast.Expr, ok bool) {
	switch no := n.(type) {
	case *ast.AssignStmt:
		for _, lhs := range no.Lhs {
			if id, ehIdent := lhs.(*ast.Ident); ehIdent {
				destinos = append(destinos, id)
			}
		}
		return destinos, no.Rhs, true
	case *ast.ValueSpec:
		return no.Names, no.Values, true
	}
	return nil, nil, false
}

// citaAlgumNome diz se a expressão usa algum identificador do conjunto.
func citaAlgumNome(e ast.Expr, nomes map[string]bool) bool {
	cita := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && nomes[id.Name] {
			cita = true
		}
		return !cita
	})
	return cita
}
