package notify

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
)

// testdata/causas.json é a lista de causas que um incidente pode ter: os
// prober.ErrorKind e as causas de tarefa. O painel lê o mesmo arquivo em
// web/src/lib/api/mensagens.test.ts. Um kind novo no sondador tem de entrar
// na lista, e daí ganhar texto aqui e na tela; sem isso, o aviso cairia em
// "erro de rede" e a tela em "Falha".
func lerCausas(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "causas.json"))
	if err != nil {
		t.Fatal(err)
	}
	var causas []string
	if err := json.Unmarshal(b, &causas); err != nil {
		t.Fatal(err)
	}
	return causas
}

func TestSharedCausesAreTheProberKindsAndTheTaskCauses(t *testing.T) {
	quer := append(errorKindsDoSondador(t), heartbeat.CauseLate, heartbeat.CauseFailed)
	slices.Sort(quer)
	lidas := lerCausas(t)
	slices.Sort(lidas)
	if !slices.Equal(lidas, quer) {
		t.Errorf("testdata/causas.json = %v; o código tem %v", lidas, quer)
	}
}

func TestEveryCauseHasItsOwnText(t *testing.T) {
	for _, c := range lerCausas(t) {
		if _, ok := causas[c]; !ok {
			t.Errorf("causa %q sem texto no aviso", c)
		}
	}
}

// errorKindsDoSondador lê do código-fonte de prober os valores das constantes
// ErrorKind: o Go não enumera constantes em tempo de execução.
func errorKindsDoSondador(t *testing.T) []string {
	t.Helper()
	arquivos, err := filepath.Glob(filepath.Join("..", "prober", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var valores []string
	for _, caminho := range arquivos {
		if strings.HasSuffix(caminho, "_test.go") {
			continue
		}
		arquivo, err := parser.ParseFile(token.NewFileSet(), caminho, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range arquivo.Decls {
			if bloco, ok := decl.(*ast.GenDecl); ok && bloco.Tok == token.CONST {
				valores = append(valores, errorKindsDoBloco(t, bloco)...)
			}
		}
	}
	if len(valores) == 0 {
		t.Fatal("nenhuma constante ErrorKind em internal/prober")
	}
	return valores
}

// errorKindsDoBloco só lê a forma X ErrorKind = "a", a de classify.go. Outra
// forma derruba o teste, em vez de deixar a constante de fora.
func errorKindsDoBloco(t *testing.T, bloco *ast.GenDecl) []string {
	t.Helper()
	var valores []string
	for _, spec := range bloco.Specs {
		declaracao, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if tipo, ok := declaracao.Type.(*ast.Ident); !ok || tipo.Name != "ErrorKind" {
			continue
		}
		for _, expr := range declaracao.Values {
			literal, ok := expr.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Fatalf("constante ErrorKind sem literal de texto em %v", declaracao.Names)
			}
			texto, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			valores = append(valores, texto)
		}
	}
	return valores
}
