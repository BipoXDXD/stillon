package storage_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	comentarioDeLinha = regexp.MustCompile(`--[^\n]*`)
	comentarioDeBloco = regexp.MustCompile(`(?s)/\*.*?\*/`)
	contagemTotal     = regexp.MustCompile(`(?i)\bcount\s*\(\s*\*\s*\)`)
	// `*` logo depois de SELECT/RETURNING (com alias opcional) ou depois de
	// uma vírgula e seguido de vírgula, FROM ou fim: sempre projeção, nunca
	// multiplicação.
	estrelaNaProjecao = regexp.MustCompile(
		`(?is)\b(?:select|returning)\s+(?:distinct\s+)?(?:\w+\.)?\*|,\s*(?:\w+\.)?\*\s*(?:,|\bfrom\b|;|$)`)
)

// acharEstrelaNaConsulta devolve um achado "arquivo:linha: trecho" para cada
// `*` de projeção (SELECT *, alias.*, RETURNING *) no SQL. count(*), a
// multiplicação e os comentários não contam.
func acharEstrelaNaConsulta(arquivo, sql string) []string {
	limpo := apagarPreservandoLinhas(comentarioDeBloco, sql)
	limpo = apagarPreservandoLinhas(comentarioDeLinha, limpo)
	limpo = apagarPreservandoLinhas(contagemTotal, limpo)
	var achados []string
	for _, pos := range estrelaNaProjecao.FindAllStringIndex(limpo, -1) {
		linha := 1 + strings.Count(limpo[:pos[0]], "\n")
		trecho := strings.Join(strings.Fields(limpo[pos[0]:pos[1]]), " ")
		achados = append(achados, fmt.Sprintf("%s:%d: %s", arquivo, linha, trecho))
	}
	return achados
}

// apagarPreservandoLinhas troca cada trecho achado por espaços, mantendo as
// quebras de linha, para a numeração das linhas continuar a do arquivo.
func apagarPreservandoLinhas(re *regexp.Regexp, texto string) string {
	return re.ReplaceAllStringFunc(texto, func(trecho string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, trecho)
	})
}

func TestSqlcStarCheckFlagsStars(t *testing.T) {
	casos := map[string]string{
		"select estrela":               "SELECT * FROM t",
		"minúsculas":                   "select * from t",
		"alias estrela":                "SELECT t.* FROM t",
		"alias depois de coluna":       "SELECT id, t.* FROM t",
		"returning estrela":            "INSERT INTO t (a) VALUES ($1)\nRETURNING *",
		"distinct estrela":             "SELECT DISTINCT * FROM t",
		"estrela em linha própria":     "SELECT\n  * FROM t",
		"estrela depois de comentário": "-- name: X :one\nSELECT * FROM t",
	}
	for nome, sql := range casos {
		t.Run(nome, func(t *testing.T) {
			achados := acharEstrelaNaConsulta("x.sql", sql)
			if len(achados) == 0 {
				t.Fatalf("estrela aceita: %q", sql)
			}
			if !strings.HasPrefix(achados[0], "x.sql:") {
				t.Errorf("o achado não cita arquivo e linha: %q", achados[0])
			}
		})
	}
}

func TestSqlcStarCheckCitesTheRightLine(t *testing.T) {
	sql := "-- name: A :one\nSELECT id FROM t;\n\n-- name: B :one\nSELECT * FROM t;"
	achados := acharEstrelaNaConsulta("x.sql", sql)
	if len(achados) != 1 || !strings.HasPrefix(achados[0], "x.sql:5:") {
		t.Fatalf("esperado um achado na linha 5, veio %q", achados)
	}
}

func TestSqlcStarCheckAcceptsLegitUses(t *testing.T) {
	casos := map[string]string{
		"count estrela":         "SELECT count(*) FROM t",
		"count com filter":      "SELECT (count(*) FILTER (WHERE c.status <> 'down'))::int FROM c",
		"multiplicação":         "SELECT a * b FROM t",
		"multiplicação colada":  "SELECT a*b FROM t",
		"comentário de linha":   "-- SELECT * FROM t\nSELECT id FROM t",
		"comentário de bloco":   "/* SELECT * */ SELECT id FROM t",
		"sqlc.embed":            "SELECT sqlc.embed(h), x.id FROM h",
		"colunas explícitas":    "SELECT id, name FROM t",
		"returning com colunas": "UPDATE t SET a = 1 RETURNING id, a",
	}
	for nome, sql := range casos {
		t.Run(nome, func(t *testing.T) {
			if achados := acharEstrelaNaConsulta("x.sql", sql); len(achados) != 0 {
				t.Errorf("uso legítimo acusado: %q em %q", achados, sql)
			}
		})
	}
}

// TestSqlcQueriesHaveNoStar aplica a regra do projeto (nada de SELECT * em
// produção) às queries reais: o sqlc expande o `*` no Go gerado, então a
// coluna nova de uma migration mudaria o tipo de retorno sem ninguém ver.
func TestSqlcQueriesHaveNoStar(t *testing.T) {
	caminhos, err := filepath.Glob("queries/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(caminhos) == 0 {
		t.Fatal("nenhuma query em queries/: o caminho do teste está errado")
	}
	var achados []string
	for _, c := range caminhos {
		conteudo, err := os.ReadFile(c)
		if err != nil {
			t.Fatal(err)
		}
		achados = append(achados, acharEstrelaNaConsulta(c, string(conteudo))...)
	}
	if len(achados) > 0 {
		t.Fatalf("`*` na projeção das queries (liste as colunas):\n%s", strings.Join(achados, "\n"))
	}
}
