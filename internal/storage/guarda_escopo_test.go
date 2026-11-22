package storage_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
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

// chamadoresDeInUserTx são os .go de produção que podem abrir uma transação
// com só o escopo do usuário (storage.InUserTx), cada um com o motivo. São os
// dados da própria pessoa, que não pertencem a projeto nenhum. Recurso de
// projeto sob InUserTx deixa de aparecer quando as permissivas forem por
// projeto (PR 2b): a consulta devolve vazio em silêncio e a contagem dá zero.
var chamadoresDeInUserTx = map[string]string{
	"internal/account/account.go":       "conta e perfil da pessoa",
	"internal/auth/provas.go":           "provas de família de sessão (00029)",
	"internal/tenancy/pgstore/store.go": "projects e project_members: a lista e o Projeto padrão da pessoa",
	"internal/storage/scope.go":         "definição",
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
	oque    string // ScopeToProject, ScopeToUser, InUserTx ou "app.project_id"
}

// usosDeEscopo varre os .go de produção sob raiz (sem _test.go, sem o código
// gerado de internal/storage/db e sem diretórios ocultos) e devolve cada
// identificador ScopeToProject, ScopeToUser ou InUserTx e cada literal de string com
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
				if no.Name == "ScopeToProject" || no.Name == "ScopeToUser" || no.Name == "InUserTx" {
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

// O escopo só do usuário (InUserTx) fica nos contextos pessoais da lista. Um
// recurso de projeto lido ou contado sob InUserTx funciona hoje e passa a
// devolver vazio em silêncio quando as permissivas forem por projeto, com o
// binário anterior ainda rodando no release.
func TestInUserTxOnlyInPersonalContexts(t *testing.T) {
	raiz, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	vistos := 0
	for _, u := range usosDeEscopo(t, raiz) {
		if u.oque != "InUserTx" {
			continue
		}
		vistos++
		if _, ok := chamadoresDeInUserTx[u.arquivo]; !ok {
			t.Errorf("%s:%d usa InUserTx fora dos contextos pessoais (%s). Recurso de projeto "+
				"vai em storage.InProjectTx; se o dado for mesmo da pessoa, acrescente o arquivo a "+
				"chamadoresDeInUserTx com o motivo.",
				u.arquivo, u.linha, strings.Join(slices.Sorted(maps.Keys(chamadoresDeInUserTx)), ", "))
		}
	}
	// Sem achar nem a definição em scope.go, a varredura não olhou nada.
	if vistos == 0 {
		t.Errorf("a varredura de %s não achou nenhum InUserTx; ela deixou de enxergar o código", raiz)
	}
}

// gucDeProjeto casa as escritas em app.project_id: set_config('app.project_id',
// ...) e SET [LOCAL|SESSION] app.project_id. Ler (current_setting) não casa.
var gucDeProjeto = regexp.MustCompile(
	`(?i)set_config\s*\(\s*'app\.project_id'|\bSET\s+(?:LOCAL\s+|SESSION\s+)?app\.project_id\b`)

// achadosDeGUCDeProjeto devolve um achado "arquivo:linha: trecho" para cada
// linha do SQL que escreve em app.project_id. Comentários não contam.
func achadosDeGUCDeProjeto(arquivo, sql string) []string {
	limpo := apagarPreservandoLinhas(comentarioDeBloco, sql)
	limpo = apagarPreservandoLinhas(comentarioDeLinha, limpo)
	var achados []string
	for i, linha := range strings.Split(limpo, "\n") {
		if gucDeProjeto.MatchString(linha) {
			achados = append(achados, fmt.Sprintf("%s:%d: %s", arquivo, i+1, strings.TrimSpace(linha)))
		}
	}
	return achados
}

// O SQL espelho de TestOnlyScopeGoSetsProjectScope: app.project_id só é
// ligado pela query ScopeToProject, que só InProjectTx chama depois de
// conferir a pertença. Um set_config numa função, num gatilho ou noutra query
// liga o escopo de projeto sem essa conferência.
func TestOnlyScopeSQLSetsProjectGUC(t *testing.T) {
	if n := len(achadosDeGUCDeProjeto("x.sql", "SELECT set_config('app.project_id', $1, true);")); n != 1 {
		t.Fatalf("o caso positivo deu %d achados, esperado 1: a regex não enxerga set_config", n)
	}
	if n := len(achadosDeGUCDeProjeto("x.sql", "BEGIN;\nSET LOCAL app.project_id = 'x';")); n != 1 {
		t.Fatalf("o caso positivo deu %d achados, esperado 1: a regex não enxerga SET LOCAL", n)
	}

	var caminhos []string
	for _, padrao := range []string{"queries/*.sql", "migrations/*.sql"} {
		achados, err := filepath.Glob(padrao)
		if err != nil {
			t.Fatal(err)
		}
		if len(achados) == 0 {
			t.Fatalf("nenhum arquivo em %s: o caminho do teste está errado", padrao)
		}
		caminhos = append(caminhos, achados...)
	}

	viuScopeToProject := false
	for _, c := range caminhos {
		conteudo, err := os.ReadFile(c)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(conteudo)
		for _, achado := range achadosDeGUCDeProjeto(c, sql) {
			if c == filepath.Join("queries", "scope.sql") && blocoDaLinha(sql, linhaDoAchado(t, achado)) == "ScopeToProject" {
				viuScopeToProject = true
				continue
			}
			t.Errorf("%s escreve em app.project_id fora do bloco ScopeToProject de queries/scope.sql. "+
				"Só storage.InProjectTx liga o escopo de projeto, depois de conferir a pertença; "+
				"ligar aqui abre as linhas de um projeto a quem não é membro.", achado)
		}
	}
	if !viuScopeToProject {
		t.Error("a varredura não achou o set_config de ScopeToProject em queries/scope.sql; ela deixou de enxergar o SQL")
	}
}

// linhaDoAchado lê o número da linha de um achado "arquivo:linha: trecho".
func linhaDoAchado(t *testing.T, achado string) int {
	t.Helper()
	partes := strings.SplitN(achado, ":", 3)
	if len(partes) < 3 {
		t.Fatalf("achado fora do formato arquivo:linha: trecho: %q", achado)
	}
	n, err := strconv.Atoi(partes[1])
	if err != nil {
		t.Fatalf("linha do achado %q: %v", achado, err)
	}
	return n
}
