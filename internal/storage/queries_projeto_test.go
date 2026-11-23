package storage_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	// nomeDaQuery casa a linha que abre um bloco do sqlc.
	nomeDaQuery = regexp.MustCompile(`^--\s*name:\s*(\w+)`)
	// filtroPorUsuario casa um filtro por user_id contra um parâmetro: o
	// recurso escolhido pela pessoa, não pelo projeto.
	filtroPorUsuario = regexp.MustCompile(`\buser_id\s*=\s*(\$\d+|sqlc\.n?arg\()`)
)

// arquivosDeRecursoDeProjeto são as queries das tabelas que pertencem a um
// projeto.
var arquivosDeRecursoDeProjeto = []string{
	"targets.sql", "heartbeats.sql", "channels.sql", "statuspages.sql", "incidents.sql", "notifications.sql",
}

// queriesQueFiltramPorUsuario são as queries de recurso de projeto que podem
// filtrar por user_id, cada uma com o motivo. Vazia: as cotas contam pelo
// titular do projeto (funções cota_* da 00045).
var queriesQueFiltramPorUsuario = map[string]string{}

// blocoDeQuery é um bloco "-- name:" de um arquivo de queries.
type blocoDeQuery struct {
	nome   string
	inicio int // linha do "-- name:", a partir de 1
	texto  string
}

// blocosDeQuery parte o SQL nos blocos "-- name:". O que vem antes do
// primeiro (o cabeçalho do arquivo) fica de fora.
func blocosDeQuery(sql string) []blocoDeQuery {
	var blocos []blocoDeQuery
	var linhas []string
	fechar := func() {
		if len(blocos) > 0 {
			blocos[len(blocos)-1].texto = strings.Join(linhas, "\n")
		}
	}
	for i, linha := range strings.Split(sql, "\n") {
		if m := nomeDaQuery.FindStringSubmatch(linha); m != nil {
			fechar()
			blocos = append(blocos, blocoDeQuery{nome: m[1], inicio: i + 1})
			linhas = nil
		}
		linhas = append(linhas, linha)
	}
	fechar()
	return blocos
}

// blocoDaLinha devolve o nome do bloco "-- name:" que contém a linha (a partir
// de 1), ou "" se ela vem antes do primeiro bloco.
func blocoDaLinha(sql string, linha int) string {
	nome := ""
	for _, b := range blocosDeQuery(sql) {
		if b.inicio > linha {
			break
		}
		nome = b.nome
	}
	return nome
}

// achadosDeFiltroPorUsuario devolve, por nome de bloco, um achado "arquivo:linha: Nome: trecho" para cada bloco do
// SQL que filtra por user_id contra um parâmetro. Comentários não contam.
func achadosDeFiltroPorUsuario(arquivo, sql string) map[string]string {
	achados := map[string]string{}
	for _, b := range blocosDeQuery(sql) {
		limpo := apagarPreservandoLinhas(comentarioDeBloco, b.texto)
		limpo = apagarPreservandoLinhas(comentarioDeLinha, limpo)
		if m := filtroPorUsuario.FindString(limpo); m != "" {
			achados[b.nome] = fmt.Sprintf("%s:%d: %s: %s", arquivo, b.inicio, b.nome, m)
		}
	}
	return achados
}

// Recurso de projeto é escolhido pelo project_id do escopo, nunca pelo
// user_id de quem pede: com membros, user_id é quem criou a linha, e filtrar
// por ele esconde o que os outros membros criaram e mostra o que a pessoa
// criou em outro projeto. Nem as cotas: elas contam pelo titular do projeto.
func TestResourceQueriesFilterByProject(t *testing.T) {
	exemplo := "-- name: ListarAlgo :many\n-- user_id = $9 no comentário não conta\nSELECT id FROM targets\n WHERE user_id = $1;\n\n" +
		"-- name: OutraCoisa :one\nSELECT id FROM targets WHERE project_id = $1;\n\n" +
		"-- name: ComArg :one\nSELECT id FROM targets WHERE user_id = sqlc.arg(user_id);"
	positivo := achadosDeFiltroPorUsuario("x.sql", exemplo)
	if len(positivo) != 2 || positivo["ListarAlgo"] == "" || positivo["ComArg"] == "" {
		t.Fatalf("o exemplo deveria acusar ListarAlgo e ComArg, veio %q", positivo)
	}

	vistos := map[string]bool{}
	for _, arquivo := range arquivosDeRecursoDeProjeto {
		caminho := filepath.Join("queries", arquivo)
		conteudo, err := os.ReadFile(caminho)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(conteudo)
		if len(blocosDeQuery(sql)) == 0 {
			t.Fatalf("%s sem bloco -- name:; o caminho ou o parser está errado", caminho)
		}
		for nome, achado := range achadosDeFiltroPorUsuario(caminho, sql) {
			if _, ok := queriesQueFiltramPorUsuario[nome]; ok {
				vistos[nome] = true
				continue
			}
			t.Errorf("%s filtra recurso de projeto por user_id. Filtre por project_id do escopo "+
				"(storage.InProjectTx); se o filtro por usuário for mesmo o certo, acrescente a query a "+
				"queriesQueFiltramPorUsuario com o motivo.", achado)
		}
	}
	// A lista não guarda exceção vencida: quando uma query deixa de filtrar
	// por user_id, o nome sai daqui. Isso também prova que a varredura achou
	// os filtros que existem.
	for _, nome := range slices.Sorted(maps.Keys(queriesQueFiltramPorUsuario)) {
		if !vistos[nome] {
			t.Errorf("%s está em queriesQueFiltramPorUsuario (%s) mas não filtra mais por user_id; tire da lista",
				nome, queriesQueFiltramPorUsuario[nome])
		}
	}
}
