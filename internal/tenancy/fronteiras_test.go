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

// TestTenancyImportsNoInfrastructure garante que o contexto continua puro:
// só biblioteca padrão, uuid e o relógio do projeto.
func TestTenancyImportsNoInfrastructure(t *testing.T) {
	permitidos := map[string]bool{
		"github.com/google/uuid":                     true,
		"github.com/BipoXDXD/stillon/internal/clock": true,
	}
	// Da biblioteca padrão, a infraestrutura também fica de fora.
	proibidos := []string{"net/http", "database/sql"}
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
			primeiro := strings.SplitN(caminho, "/", 2)[0]
			padrao := !strings.Contains(primeiro, ".")
			for _, pr := range proibidos {
				if caminho == pr || strings.HasPrefix(caminho, pr+"/") {
					t.Errorf("%s importa %q: tenancy não conhece HTTP nem banco", arq, caminho)
				}
			}
			if !padrao && !permitidos[caminho] {
				t.Errorf("%s importa %q: tenancy só pode usar a biblioteca padrão, uuid e internal/clock", arq, caminho)
			}
		}
	}
	if vistos == 0 {
		t.Fatal("nenhum arquivo não-teste encontrado")
	}
}
