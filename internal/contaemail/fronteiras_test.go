package contaemail_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const caminhoContaemail = "github.com/BipoXDXD/stillon/internal/contaemail"

// importaContaemail diz se a lista de imports traz este pacote.
func importaContaemail(imports []string) bool {
	for _, imp := range imports {
		if imp == caminhoContaemail || strings.HasPrefix(imp, caminhoContaemail+"/") {
			return true
		}
	}
	return false
}

// Os avisos de incidente (notificadores, Resend.Send) nunca passam pelo teto
// dos e-mails de conta: o notify não importa o contaemail, nem nos testes.
func TestNotifyNaoImportaContaemail(t *testing.T) {
	// Caso positivo embutido: sem ele, uma regra quebrada passaria calada.
	if !importaContaemail([]string{"context", caminhoContaemail}) {
		t.Fatal("a regra não reconhece o import do contaemail")
	}
	if importaContaemail([]string{"context", "github.com/BipoXDXD/stillon/internal/crypto"}) {
		t.Fatal("a regra recusa um import alheio")
	}

	arquivos, err := filepath.Glob(filepath.Join("..", "notify", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, arq := range arquivos {
		src, err := os.ReadFile(arq)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, arq, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		var imports []string
		for _, imp := range f.Imports {
			caminho, _ := strconv.Unquote(imp.Path.Value)
			imports = append(imports, caminho)
		}
		if importaContaemail(imports) {
			t.Errorf("%s importa o contaemail: os avisos passariam pelo teto", arq)
		}
	}
	if len(arquivos) == 0 {
		t.Fatal("nenhum arquivo do notify encontrado")
	}
}
