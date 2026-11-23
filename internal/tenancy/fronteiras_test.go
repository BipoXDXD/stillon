package tenancy

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// permitidos são os únicos pacotes fora da biblioteca padrão que o tenancy
// pode importar: uuid, o relógio e a regra de texto enganoso (internal/rotulo,
// só biblioteca padrão).
var permitidos = map[string]bool{
	"github.com/google/uuid":                      true,
	"github.com/BipoXDXD/stillon/internal/clock":  true,
	"github.com/BipoXDXD/stillon/internal/rotulo": true,
}

// Da biblioteca padrão, a infraestrutura também fica de fora.
var proibidos = []string{"net/http", "database/sql"}

// motivoDoImport diz por que o tenancy não pode importar caminho, ou "" se pode.
func motivoDoImport(caminho string) string {
	for _, pr := range proibidos {
		if caminho == pr || strings.HasPrefix(caminho, pr+"/") {
			return "tenancy não conhece HTTP nem banco"
		}
	}
	primeiro := strings.SplitN(caminho, "/", 2)[0]
	padrao := !strings.Contains(primeiro, ".")
	if !padrao && !permitidos[caminho] {
		return "tenancy só pode usar a biblioteca padrão, uuid, internal/clock e internal/rotulo"
	}
	return ""
}

// TestTenancyImportsNoInfrastructure garante que o contexto continua puro:
// só biblioteca padrão, uuid, o relógio e a regra de texto enganoso.
func TestTenancyImportsNoInfrastructure(t *testing.T) {
	// Caso positivo embutido: a regra ainda recusa a infraestrutura e aceita
	// o que foi liberado; sem ele, uma regra quebrada passaria calada.
	for caminho, recusa := range map[string]bool{
		"github.com/BipoXDXD/stillon/internal/storage": true,
		"net/http": true,
		"github.com/BipoXDXD/stillon/internal/rotulo": false,
		"strings": false,
	} {
		if got := motivoDoImport(caminho) != ""; got != recusa {
			t.Errorf("motivoDoImport(%q) recusa = %v; quer %v", caminho, got, recusa)
		}
	}
	arquivos, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	vistos := 0
	fset := token.NewFileSet()
	for _, arq := range arquivos {
		if strings.HasSuffix(arq, "_test.go") {
			continue
		}
		src, err := os.ReadFile(arq)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, arq, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		vistos++
		for _, imp := range f.Imports {
			caminho, _ := strconv.Unquote(imp.Path.Value)
			if motivo := motivoDoImport(caminho); motivo != "" {
				t.Errorf("%s importa %q: %s", arq, caminho, motivo)
			}
		}
	}
	if vistos == 0 {
		t.Fatal("nenhum arquivo não-teste encontrado")
	}
}
