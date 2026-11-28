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
// fechada de propósito: as permissivas são por projeto, e uma transação com só
// app.user_id não vê nem grava recurso de projeto nenhum. Um chamador novo que
// lesse recurso assim receberia vazio em silêncio; o caminho é
// storage.InProjectTx, que confere a pertença antes de ligar o projeto.
var chamadoresDeScopeToUser = []string{
	"internal/account/account.go",
	// signup: o usuário nasce na transação e o escopo é ligado para o Projeto
	// padrão e o token.
	"internal/account/cadastro.go",
	// reset: o dono só é conhecido depois de consumir o token.
	"internal/account/recuperacao.go",
	"internal/auth/store.go",
	"internal/auth/totp.go",
	"internal/storage/scope.go",
}

// chamadoresDeInUserTx são os .go de produção que podem abrir uma transação
// com só o escopo do usuário (storage.InUserTx), cada um com o motivo. São os
// dados da própria pessoa, que não pertencem a projeto nenhum. Recurso de
// projeto sob InUserTx não aparece, porque as permissivas são por projeto: a
// consulta devolve vazio em silêncio e a contagem dá zero.
var chamadoresDeInUserTx = map[string]string{
	"internal/account/account.go":       "conta e perfil da pessoa",
	"internal/account/cadastro.go":      "emissão de confirmação do endereço da própria conta",
	"internal/account/recuperacao.go":   "emissão de redefinição da senha da conta",
	"internal/account/tokens.go":        "conclusão da entrega dos links da conta",
	"internal/auth/provas.go":           "provas de família de sessão (00029)",
	"internal/tenancy/pgstore/store.go": "projects e project_members: a lista e o Projeto padrão da pessoa",
	"internal/storage/scope.go":         "definição",
}

// chamadoresDeInProjectPgxTx são os .go de produção que podem usar
// storage.InProjectPgxTx, o pgx.Tx cru com o escopo de projeto ligado. Fora da
// definição e do InProjectTx (scope.go), só a semente de teste (testdb): SQL
// cru em código de produção fugiria das queries geradas, que a revisão e os
// testes de query cobrem.
var chamadoresDeInProjectPgxTx = map[string]string{
	"internal/storage/scope.go":          "definição e InProjectTx",
	"internal/storage/testdb/projeto.go": "sementes de teste no escopo de projeto",
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
	oque    string // ScopeToProject, ScopeToUser, InUserTx, InProjectPgxTx ou "app.project_id"
}

// usosDeEscopo varre os .go de produção sob raiz (sem _test.go, sem o código
// gerado de internal/storage/db e sem diretórios ocultos) e devolve cada
// identificador ScopeToProject, ScopeToUser, InUserTx ou InProjectPgxTx e cada literal de string com
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
				if no.Name == "ScopeToProject" || no.Name == "ScopeToUser" || no.Name == "InUserTx" ||
					no.Name == "InProjectPgxTx" {
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
				t.Errorf("%s:%d chama ScopeToUser fora da lista conhecida (%s). As permissivas "+
					"são por projeto: uma transação com só app.user_id não vê recurso de projeto, e a "+
					"consulta devolve vazio em silêncio. Use storage.InProjectTx; se o escopo de usuário "+
					"for mesmo o certo, justifique e acrescente o arquivo a chamadoresDeScopeToUser.",
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
// recurso de projeto lido ou contado sob InUserTx devolve vazio em silêncio:
// as permissivas são por projeto, e só InProjectTx liga app.project_id.
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

// O pgx.Tx cru com o escopo de projeto fica na lista fechada: um chamador novo
// em produção escreveria SQL fora das queries geradas, sem a revisão delas.
func TestInProjectPgxTxOnlyInKnownCallers(t *testing.T) {
	raiz, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	vistos := 0
	for _, u := range usosDeEscopo(t, raiz) {
		if u.oque != "InProjectPgxTx" {
			continue
		}
		vistos++
		if _, ok := chamadoresDeInProjectPgxTx[u.arquivo]; !ok {
			t.Errorf("%s:%d usa InProjectPgxTx fora da lista conhecida (%s). Use storage.InProjectTx "+
				"com as queries geradas; se o SQL cru for mesmo necessário, acrescente o arquivo a "+
				"chamadoresDeInProjectPgxTx com o motivo.",
				u.arquivo, u.linha, strings.Join(slices.Sorted(maps.Keys(chamadoresDeInProjectPgxTx)), ", "))
		}
	}
	// Sem achar nem a definição em scope.go, a varredura não olhou nada.
	if vistos == 0 {
		t.Errorf("a varredura de %s não achou nenhum InProjectPgxTx; ela deixou de enxergar o código", raiz)
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

// migrationDaCota é a das funções de cota do titular: a única, fora de
// ScopeToProject, que escreve em app.project_id, e só para zerá-lo durante a
// contagem e devolvê-lo ao valor que guardou (nenhuma liga projeto novo).
var migrationDaCota = filepath.Join("migrations", "00045_contagem_de_cota_do_titular.sql")

var (
	// escritaDaCota casa as duas formas aceitas na 00045: set_config local de
	// app.project_id para a string vazia (grupo 1) ou para uma variável
	// (grupo 2), que tem de ser a que guardou o valor (guardaDoEscopo).
	escritaDaCota = regexp.MustCompile(
		`(?i)set_config\s*\(\s*'app\.project_id'\s*,\s*(?:('')|([a-z_][a-z0-9_]*))\s*,\s*true\s*\)`)
	// guardaDoEscopo casa a atribuição que guarda app.project_id numa
	// variável (grupo 1) antes de zerá-lo.
	guardaDoEscopo = regexp.MustCompile(
		`(?i)\b([a-z_][a-z0-9_]*)\s*:=\s*COALESCE\s*\(\s*current_setting\s*\(\s*'app\.project_id'\s*,\s*true\s*\)\s*,\s*''\s*\)`)
)

// semEscritasDaCota tira do SQL (e dos comentários) cada escrita aceita em
// app.project_id e diz quantas zeram e quantas restauram. A restauração só é
// aceita com uma variável que o mesmo SQL guardou com guardaDoEscopo: outra
// variável, outro literal, set_config não local ou a cláusula SET de função
// ficam no SQL e viram achado.
func semEscritasDaCota(sql string) (semEscritas string, zeram, restauram int) {
	limpo := apagarPreservandoLinhas(comentarioDeBloco, sql)
	limpo = apagarPreservandoLinhas(comentarioDeLinha, limpo)
	guardadas := map[string]bool{}
	for _, m := range guardaDoEscopo.FindAllStringSubmatch(limpo, -1) {
		guardadas[strings.ToLower(m[1])] = true
	}
	var b strings.Builder
	desde := 0
	for _, m := range escritaDaCota.FindAllStringSubmatchIndex(limpo, -1) {
		switch {
		case m[2] >= 0:
			zeram++
		case guardadas[strings.ToLower(limpo[m[4]:m[5]])]:
			restauram++
		default:
			continue
		}
		b.WriteString(limpo[desde:m[0]])
		b.WriteString(apagarPreservandoLinhas(escritaDaCota, limpo[m[0]:m[1]]))
		desde = m[1]
	}
	b.WriteString(limpo[desde:])
	return b.String(), zeram, restauram
}

// O SQL espelho de TestOnlyScopeGoSetsProjectScope: app.project_id só é
// ligado pela query ScopeToProject, que só InProjectTx chama depois de
// conferir a pertença. Um set_config numa função, num gatilho ou noutra query
// liga o escopo de projeto sem essa conferência. A exceção são as funções de
// cota da 00045, que o zeram e o devolvem ao valor guardado, sem ligar
// projeto nenhum.
func TestOnlyScopeSQLSetsProjectGUC(t *testing.T) {
	if n := len(achadosDeGUCDeProjeto("x.sql", "SELECT set_config('app.project_id', $1, true);")); n != 1 {
		t.Fatalf("o caso positivo deu %d achados, esperado 1: a regex não enxerga set_config", n)
	}
	if n := len(achadosDeGUCDeProjeto("x.sql", "BEGIN;\nSET LOCAL app.project_id = 'x';")); n != 1 {
		t.Fatalf("o caso positivo deu %d achados, esperado 1: a regex não enxerga SET LOCAL", n)
	}
	const guarda = "escopo := COALESCE(current_setting('app.project_id', true), '');\n"
	for _, c := range []struct {
		nome, sql                 string
		zeram, restauram, achados int
	}{
		{"zera", "PERFORM set_config('app.project_id', '', true);", 1, 0, 0},
		{"restaura o guardado", guarda + "PERFORM set_config('app.project_id', escopo, true);", 0, 1, 0},
		{"variável não guardada", guarda + "PERFORM set_config('app.project_id', outro, true);", 0, 0, 1},
		{"literal com valor", "PERFORM set_config('app.project_id', 'x', true);", 0, 0, 1},
		{"não local", "PERFORM set_config('app.project_id', '', false);", 0, 0, 1},
		{"cláusula de função", "CREATE FUNCTION f() RETURNS int LANGUAGE sql\n" +
			"SET app.project_id = '' AS $$ SELECT 1 $$;", 0, 0, 1},
	} {
		sql, zeram, restauram := semEscritasDaCota(c.sql)
		achados := len(achadosDeGUCDeProjeto("x.sql", sql))
		if zeram != c.zeram || restauram != c.restauram || achados != c.achados {
			t.Fatalf("%s: %d zera(m), %d restaura(m) e %d achado(s); esperado %d, %d e %d",
				c.nome, zeram, restauram, achados, c.zeram, c.restauram, c.achados)
		}
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

	viuScopeToProject, zeramNaCota, restauramNaCota := false, 0, 0
	for _, c := range caminhos {
		conteudo, err := os.ReadFile(c)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(conteudo)
		if c == migrationDaCota {
			sql, zeramNaCota, restauramNaCota = semEscritasDaCota(sql)
		}
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
	if zeramNaCota == 0 || restauramNaCota == 0 {
		t.Errorf("a varredura achou %d escrita(s) que zeram e %d que restauram app.project_id em %s; "+
			"queria as duas formas: ela deixou de enxergar a migration", zeramNaCota, restauramNaCota, migrationDaCota)
	}
}

// gucsDaCota são os GUCs que abrem os projetos do titular às políticas
// <t>_cota_select. Só as funções da 00045 os ligam, e elas os zeram de novo
// antes de sair.
var gucsDaCota = []string{"app.cota_titular_id", "app.cota_projetos"}

// Ligar app.cota_* fora das funções de cota abriria, a quem só enxerga um
// projeto, os recursos de todos os projetos do titular. Nenhuma query, nenhuma
// outra migration e nenhum .go de produção os cita, nem em comentário.
func TestOnlyQuotaFunctionsUseQuotaGUCs(t *testing.T) {
	cita := func(texto string) string {
		for _, guc := range gucsDaCota {
			if strings.Contains(texto, guc) {
				return guc
			}
		}
		return ""
	}

	viuNaMigration := false
	for _, padrao := range []string{"queries/*.sql", "migrations/*.sql"} {
		caminhos, err := filepath.Glob(padrao)
		if err != nil {
			t.Fatal(err)
		}
		if len(caminhos) == 0 {
			t.Fatalf("nenhum arquivo em %s: o caminho do teste está errado", padrao)
		}
		for _, c := range caminhos {
			conteudo, err := os.ReadFile(c)
			if err != nil {
				t.Fatal(err)
			}
			guc := cita(string(conteudo))
			switch {
			case guc == "":
			case c == migrationDaCota:
				viuNaMigration = true
			default:
				t.Errorf("%s cita %s: só as funções de cota de %s ligam esse GUC", c, guc, migrationDaCota)
			}
		}
	}
	if !viuNaMigration {
		t.Errorf("a varredura não achou os GUCs da cota em %s; ela deixou de enxergar a migration", migrationDaCota)
	}

	raiz, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	lidos := 0
	err = filepath.WalkDir(raiz, func(caminho string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if caminho != raiz && (strings.HasPrefix(d.Name(), ".") || diretoriosForaDaVarredura[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(caminho, ".go") || strings.HasSuffix(caminho, "_test.go") {
			return nil
		}
		conteudo, err := os.ReadFile(caminho)
		if err != nil {
			return err
		}
		lidos++
		if guc := cita(string(conteudo)); guc != "" {
			rel, _ := filepath.Rel(raiz, caminho)
			t.Errorf("%s cita %s: só as funções de cota de %s ligam esse GUC", filepath.ToSlash(rel), guc, migrationDaCota)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("varrer %s: %v", raiz, err)
	}
	if lidos == 0 {
		t.Errorf("a varredura de %s não leu nenhum .go de produção", raiz)
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

// identificadoresDeProducao devolve, para cada nome pedido, os arquivos de
// produção (relativos à raiz, com "/") onde ele aparece como identificador,
// com a mesma varredura de usosDeEscopo: sem _test.go, sem db/ gerado e sem
// diretórios ocultos.
func identificadoresDeProducao(t *testing.T, raiz string, nomes ...string) map[string][]string {
	t.Helper()
	achados := map[string][]string{}
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
			if id, ok := n.(*ast.Ident); ok && slices.Contains(nomes, id.Name) && !slices.Contains(achados[id.Name], rel) {
				achados[id.Name] = append(achados[id.Name], rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("varrer %s: %v", raiz, err)
	}
	return achados
}

// O portão do 2b: ListTitularContentRuleTargetURLs lê as URLs dos alvos com
// regra em TODOS os projetos do titular, inclusive os que quem chama não
// enxerga (cota_urls_com_regra_do_titular liga os GUCs app.cota_*, 00045).
// Com membros, quem cria alvo num projeto alheio chega a essa consulta; ela só
// pode servir à conta do teto em internal/targets, e a linha (com a URL)
// nunca pode chegar a um DTO de internal/httpapi.
func TestQuotaURLsNeverLeaveTargetsQuota(t *testing.T) {
	raiz, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const consulta, linha = "ListTitularContentRuleTargetURLs", "ListTitularContentRuleTargetURLsRow"
	achados := identificadoresDeProducao(t, raiz, consulta, linha)
	if len(achados[consulta]) == 0 {
		t.Fatalf("a varredura de %s não achou %s; ela deixou de enxergar o código", raiz, consulta)
	}
	for _, arquivo := range achados[consulta] {
		if !strings.HasPrefix(arquivo, "internal/targets/") {
			t.Errorf("%s chama %s fora de internal/targets/: essa consulta vê as URLs de todos os "+
				"projetos do titular e só serve à conta do teto de regras por host", arquivo, consulta)
		}
	}
	for _, arquivo := range achados[linha] {
		if strings.HasPrefix(arquivo, "internal/httpapi/") {
			t.Errorf("%s usa %s: a linha traz URLs de projetos que quem pede pode não enxergar, "+
				"e não pode chegar a uma resposta da API", arquivo, linha)
		}
	}
}
