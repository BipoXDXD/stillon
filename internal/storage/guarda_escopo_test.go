package storage_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// donoDoEscopoDeProjeto é o único .go de produção que pode ligar
// app.project_id: InProjectTx confere a pertença na mesma transação antes de
// ligar. Fora dele, ligar o projeto pularia a conferência.
const donoDoEscopoDeProjeto = "internal/storage/scope.go"

// chamadoresDeScopeToUser são os .go de produção que hoje ligam só o escopo do
// usuário, cada um por um motivo conferido (conta, sessões, TOTP). A lista é
// fechada de propósito: quando as permissivas passarem a ser por projeto (PR
// 2b), uma transação nova com só app.user_id enxergaria ou gravaria linhas
// sem a conferência de pertença, e o vazamento entre projetos nasce aí.
var chamadoresDeScopeToUser = []string{
	"internal/account/account.go",
	"internal/auth/store.go",
	"internal/auth/totp.go",
	"internal/storage/scope.go",
}

// diretoriosForaDaVarredura nunca têm código de produção do módulo, e alguns
// (.claude/, .git/) guardam worktrees de outras sessões que este teste não
// pode ler.
var diretoriosForaDaVarredura = map[string]bool{
	"node_modules": true, "testdata": true, "vendor": true,
}

// usoDeEscopo é um ponto de um .go que liga escopo de RLS.
type usoDeEscopo struct {
	arquivo string // relativo à raiz do módulo, com "/"
	linha   int
	oque    string // ScopeToProject, ScopeToUser ou "app.project_id"
}

// usosDeEscopo varre os .go de produção sob raiz (sem _test.go, sem o código
// gerado de internal/storage/db e sem diretórios ocultos) e devolve cada
// identificador ScopeToProject ou ScopeToUser e cada literal de string com
// app.project_id. Lê a árvore sintática: comentário que cita o nome não conta.
func usosDeEscopo(t *testing.T, raiz string) []usoDeEscopo {
	t.Helper()
	var usos []usoDeEscopo
	fset := token.NewFileSet()
	err := filepath.WalkDir(raiz, func(caminho string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(raiz, caminho)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(d.Name(), ".") || diretoriosForaDaVarredura[d.Name()] ||
				rel == "internal/storage/db") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		arq, err := parser.ParseFile(fset, caminho, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("ler %s: %w", rel, err)
		}
		ast.Inspect(arq, func(n ast.Node) bool {
			switch no := n.(type) {
			case *ast.Ident:
				if no.Name == "ScopeToProject" || no.Name == "ScopeToUser" {
					usos = append(usos, usoDeEscopo{rel, fset.Position(no.Pos()).Line, no.Name})
				}
			case *ast.BasicLit:
				if no.Kind != token.STRING {
					return true
				}
				if texto, err := strconv.Unquote(no.Value); err == nil && strings.Contains(texto, "app.project_id") {
					usos = append(usos, usoDeEscopo{rel, fset.Position(no.Pos()).Line, "app.project_id"})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("varrer %s: %v", raiz, err)
	}
	return usos
}

// Ruling T7: só InProjectTx liga o escopo de projeto, e só a lista conhecida
// liga o escopo de usuário sem projeto. Uma chamada nova fora deles é um
// caminho até as linhas do projeto que não passa pela conferência de pertença.
func TestOnlyScopeGoSetsProjectScope(t *testing.T) {
	raiz, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	usos := usosDeEscopo(t, raiz)

	vistos := map[string]bool{}
	for _, u := range usos {
		vistos[u.oque] = true
		switch u.oque {
		case "ScopeToProject", "app.project_id":
			if u.arquivo != donoDoEscopoDeProjeto {
				t.Errorf("%s:%d usa %s; só %s pode ligar o escopo de projeto, porque "+
					"InProjectTx confere a pertença na mesma transação antes de ligar. Ligar em "+
					"outro lugar pula essa conferência e abre as linhas de um projeto a quem não é "+
					"membro. Use storage.InProjectTx.",
					u.arquivo, u.linha, u.oque, donoDoEscopoDeProjeto)
			}
		case "ScopeToUser":
			if !slices.Contains(chamadoresDeScopeToUser, u.arquivo) {
				t.Errorf("%s:%d chama ScopeToUser fora da lista conhecida (%s). Quando as "+
					"permissivas forem por projeto (PR 2b), uma transação com só app.user_id vaza "+
					"linhas entre projetos. Use storage.InProjectTx; se o escopo de usuário for "+
					"mesmo o certo, justifique e acrescente o arquivo a chamadoresDeScopeToUser.",
					u.arquivo, u.linha, strings.Join(chamadoresDeScopeToUser, ", "))
			}
		}
	}
	// Prova de que a varredura enxerga o código: sem achar nem as chamadas de
	// scope.go, o teste passaria sem olhar nada. O literal app.project_id não
	// entra aqui: hoje ele só existe na query (db/, fora da varredura).
	for _, nome := range []string{"ScopeToProject", "ScopeToUser"} {
		if !vistos[nome] {
			t.Errorf("a varredura de %s não achou nenhum %s; ela deixou de enxergar o código", raiz, nome)
		}
	}
}
