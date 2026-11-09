package storage_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const pacoteGoose = "github.com/pressly/goose/v3"

// versaoBaixa tem de ficar abaixo da maior versão e não pode ser a vaga 00015.
const versaoBaixa = 3

// TestMigrateRefusesAVersionMissingBelowTheLatest afirma o texto da recusa, e
// não só err != nil: com a recusa desligada, reaplicar a versão também falharia,
// por outro motivo (o objeto já existe).
func TestMigrateRefusesAVersionMissingBelowTheLatest(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatalf("aplicar tudo: %v", err)
	}
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrar de novo com o histórico inteiro: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tag, err := conn.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id = $1`, versaoBaixa)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("linhas apagadas de goose_db_version = %d, queria 1", tag.RowsAffected())
	}
	antes := contarVersoesAplicadas(t, conn)

	err = storage.Migrate(ctx, dsn)

	if err == nil {
		t.Fatal("Migrate aceitou o banco sem a versão baixa")
	}
	for _, trecho := range []string{"detected 1 missing (out-of-order) migration", fmt.Sprintf(": version %d", versaoBaixa)} {
		if !strings.Contains(err.Error(), trecho) {
			t.Errorf("erro = %q; queria que contivesse %q", err, trecho)
		}
	}
	if depois := contarVersoesAplicadas(t, conn); depois != antes {
		t.Errorf("goose_db_version foi de %d para %d linhas: a recusa não pode aplicar nada", antes, depois)
	}
}

func contarVersoesAplicadas(t *testing.T, conn *pgx.Conn) int {
	t.Helper()
	var total int
	if err := conn.QueryRow(context.Background(), `SELECT count(*) FROM goose_db_version`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

// O goose recusa, no Up, uma versão que está nos arquivos, falta em
// goose_db_version e é menor que a maior aplicada: o boot cai antes de servir e
// nada é aplicado. TestMigrateRefusesAVersionMissingBelowTheLatest prova isso no
// Postgres, mas só pelo caminho de Migrate. A lista cobre o que aquele teste não
// executa: qualquer outro fonte de produção que importe o goose e aceite versão
// fora de ordem, ou aplique uma versão sem passar pela recusa.
var apisQueDesligamARecusa = []string{
	"WithAllowOutofOrder",
	"WithAllowMissing",
	"WithDisableVersioning",
	"WithNoVersioning",
	"ApplyVersion",
}

func TestProductionCodeNeverDisablesOutOfOrderRefusal(t *testing.T) {
	raiz := filepath.Join("..", "..")
	arquivos := fontesQueImportamGoose(t, raiz)

	if len(arquivos) == 0 {
		t.Fatal("nenhum fonte de produção importa o goose: a varredura não está olhando o boot")
	}
	for _, arquivo := range arquivos {
		for _, uso := range usosProibidos(t, arquivo) {
			t.Errorf("%s: %s aceitaria migration fora de ordem; numere acima da maior versão", arquivo, uso)
		}
	}
}

// fontesQueImportamGoose devolve os .go de produção que importam o goose. Quem
// não importa não tem como passar as opções dele.
func fontesQueImportamGoose(t *testing.T, raiz string) []string {
	t.Helper()
	var arquivos []string
	err := filepath.WalkDir(raiz, func(caminho string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if caminho != raiz && diretorioIgnorado(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(caminho, ".go") && !strings.HasSuffix(caminho, "_test.go") && importaGoose(t, caminho) {
			arquivos = append(arquivos, caminho)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return arquivos
}

func diretorioIgnorado(nome string) bool {
	return strings.HasPrefix(nome, ".") || nome == "node_modules" || nome == "vendor" || nome == "testdata"
}

func importaGoose(t *testing.T, caminho string) bool {
	t.Helper()
	arquivo, err := parser.ParseFile(token.NewFileSet(), caminho, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ler imports de %s: %v", caminho, err)
	}
	return slices.ContainsFunc(arquivo.Imports, func(i *ast.ImportSpec) bool {
		return strings.HasPrefix(strings.Trim(i.Path.Value, `"`), pacoteGoose)
	})
}

// usosProibidos compara o nome de cada identificador, sem olhar o qualificador:
// assim um import com alias ou uma opção guardada numa variável não escapam.
func usosProibidos(t *testing.T, caminho string) []string {
	t.Helper()
	fset := token.NewFileSet()
	arquivo, err := parser.ParseFile(fset, caminho, nil, 0)
	if err != nil {
		t.Fatalf("ler %s: %v", caminho, err)
	}
	var usos []string
	ast.Inspect(arquivo, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && slices.Contains(apisQueDesligamARecusa, id.Name) {
			usos = append(usos, fmt.Sprintf("%s (linha %d)", id.Name, fset.Position(id.Pos()).Line))
		}
		return true
	})
	return usos
}

// Com a sequência contínua, um número novo menor que a maior versão só pode ser
// repetido ou a vaga da 00015, que TestMigration00015StaysVacant recusa. O PR
// que tentasse ficar fora de ordem cai aqui, em vez de cair no boot de produção.
func TestMigrationFilesAreNumberedWithoutGapsOrDuplicates(t *testing.T) {
	arquivos, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	nomes := make([]string, 0, len(arquivos))
	for _, a := range arquivos {
		nomes = append(nomes, filepath.Base(a))
	}
	if err := conferirNumeracao(nomes); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationNumberingAcceptsValidSequences(t *testing.T) {
	casos := map[string][]string{
		"sequência simples":         {"00001_a.sql", "00002_b.sql"},
		"uma migration só":          {"00001_a.sql"},
		"vaga da 00015":             append(sequenciaAte(14), "00016_a.sql", "00017_b.sql"),
		"fora de ordem na listagem": {"00002_b.sql", "00001_a.sql"},
	}
	for nome, nomes := range casos {
		t.Run(nome, func(t *testing.T) {
			if err := conferirNumeracao(nomes); err != nil {
				t.Errorf("numeração válida recusada: %v", err)
			}
		})
	}
}

func TestMigrationNumberingRefusesBrokenSequences(t *testing.T) {
	casos := map[string][]string{
		"nenhum arquivo":             {},
		"não começa em 1":            {"00002_a.sql", "00003_b.sql"},
		"versão zero":                {"00000_a.sql", "00001_b.sql"},
		"buraco comum":               {"00001_a.sql", "00003_b.sql"},
		"número repetido":            {"00001_a.sql", "00002_b.sql", "00002_c.sql"},
		"repetido com zeros a menos": {"00001_a.sql", "00002_b.sql", "2_c.sql"},
		"buraco logo após a vaga":    append(sequenciaAte(14), "00017_a.sql"),
		"buraco antes da vaga":       append(sequenciaAte(13), "00016_a.sql"),
		"sem número":                 {"00001_a.sql", "novo_b.sql"},
		"sem separador":              {"00001_a.sql", "00002.sql"},
	}
	for nome, nomes := range casos {
		t.Run(nome, func(t *testing.T) {
			if err := conferirNumeracao(nomes); err == nil {
				t.Errorf("numeração quebrada aceita: %v", nomes)
			}
		})
	}
}

// sequenciaAte devolve os nomes de 00001 até ultima, sem lacunas.
func sequenciaAte(ultima int) []string {
	nomes := make([]string, 0, ultima)
	for v := 1; v <= ultima; v++ {
		nomes = append(nomes, fmt.Sprintf("%05d_m.sql", v))
	}
	return nomes
}

var nomeDeMigration = regexp.MustCompile(`^([0-9]+)_.+\.sql$`)

// versoesVagas são os números sem arquivo de propósito: a 00015 ficou vaga para
// sempre (00016_heartbeats.sql).
var versoesVagas = map[int64]bool{15: true}

type migrationNumerada struct {
	versao int64
	nome   string
}

// conferirNumeracao exige versões únicas e contínuas a partir de 1, exceto as
// vagas. Compara o número, não o texto: 2_c.sql e 00002_b.sql são a mesma versão.
func conferirNumeracao(nomes []string) error {
	if len(nomes) == 0 {
		return errors.New("nenhuma migration encontrada")
	}
	migrations, err := numerarMigrations(nomes)
	if err != nil {
		return err
	}
	slices.SortFunc(migrations, func(a, b migrationNumerada) int { return cmp.Compare(a.versao, b.versao) })

	esperada := int64(1)
	for i, m := range migrations {
		if i > 0 && m.versao == migrations[i-1].versao {
			return fmt.Errorf("versão %d repetida: %s e %s", m.versao, migrations[i-1].nome, m.nome)
		}
		for m.versao != esperada && versoesVagas[esperada] {
			esperada++
		}
		if m.versao != esperada {
			return fmt.Errorf("%s: esperava a versão %d; numere a migration nova logo acima da maior", m.nome, esperada)
		}
		esperada++
	}
	return nil
}

func numerarMigrations(nomes []string) ([]migrationNumerada, error) {
	migrations := make([]migrationNumerada, 0, len(nomes))
	for _, nome := range nomes {
		partes := nomeDeMigration.FindStringSubmatch(nome)
		if partes == nil {
			return nil, fmt.Errorf("%s: nome fora do formato NNNNN_descricao.sql", nome)
		}
		versao, err := strconv.ParseInt(partes[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: número inválido: %w", nome, err)
		}
		migrations = append(migrations, migrationNumerada{versao: versao, nome: nome})
	}
	return migrations, nil
}
