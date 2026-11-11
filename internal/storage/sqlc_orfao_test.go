package storage_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// arquivosFixosDoSqlc são os únicos .go de db/ que não nascem de uma query: o
// sqlc os gera sempre (db.go, models.go) ou com emit_interface (querier.go).
var arquivosFixosDoSqlc = map[string]bool{"db.go": true, "models.go": true, "querier.go": true}

// conferirSqlcSemOrfaos recebe os nomes base dos .go de db/ e dos .sql de
// queries/. Todo gerado fora da allowlist tem de ser X.sql.go com X.sql
// presente; os demais são órfãos e o erro lista todos, em ordem.
func conferirSqlcSemOrfaos(gerados, consultas []string) error {
	var orfaos []string
	for _, g := range gerados {
		if arquivosFixosDoSqlc[g] {
			continue
		}
		if !strings.HasSuffix(g, ".sql.go") || !slices.Contains(consultas, strings.TrimSuffix(g, ".go")) {
			orfaos = append(orfaos, g)
		}
	}
	if len(orfaos) == 0 {
		return nil
	}
	slices.Sort(orfaos)
	return fmt.Errorf("sem query correspondente em internal/storage/queries: %s "+
		"(apague o arquivo gerado ou restaure a query e rode `sqlc generate`)",
		strings.Join(orfaos, ", "))
}

func TestSqlcOrphanCheckAcceptsMatchedFiles(t *testing.T) {
	casos := map[string]struct{ gerados, consultas []string }{
		"cada gerado com a sua query": {
			gerados:   []string{"db.go", "models.go", "querier.go", "users.sql.go", "targets.sql.go"},
			consultas: []string{"users.sql", "targets.sql"},
		},
		"só a allowlist": {
			gerados:   []string{"db.go", "models.go"},
			consultas: []string{"users.sql"},
		},
		"query sem gerado fica para o sqlc diff": {
			gerados:   []string{"db.go", "users.sql.go"},
			consultas: []string{"users.sql", "novo.sql"},
		},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			if err := conferirSqlcSemOrfaos(c.gerados, c.consultas); err != nil {
				t.Errorf("conjunto válido recusado: %v", err)
			}
		})
	}
}

func TestSqlcOrphanCheckRefusesOrphans(t *testing.T) {
	casos := map[string]struct {
		gerados, consultas []string
		citados            []string
	}{
		"gerado sem query": {
			gerados:   []string{"db.go", "users.sql.go", "velho.sql.go"},
			consultas: []string{"users.sql"},
			citados:   []string{"velho.sql.go"},
		},
		"query renomeada": {
			gerados:   []string{"db.go", "heartbeats.sql.go"},
			consultas: []string{"pulsos.sql"},
			citados:   []string{"heartbeats.sql.go"},
		},
		".go solto fora da allowlist": {
			gerados:   []string{"db.go", "helpers.go"},
			consultas: []string{"users.sql"},
			citados:   []string{"helpers.go"},
		},
		"vários órfãos aparecem todos": {
			gerados:   []string{"b.sql.go", "a.sql.go", "c.go"},
			consultas: nil,
			citados:   []string{"a.sql.go", "b.sql.go", "c.go"},
		},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			err := conferirSqlcSemOrfaos(c.gerados, c.consultas)
			if err == nil {
				t.Fatalf("órfão aceito: %v", c.gerados)
			}
			for _, arquivo := range c.citados {
				if !strings.Contains(err.Error(), arquivo) {
					t.Errorf("o erro não cita %s: %v", arquivo, err)
				}
			}
		})
	}
}

// TestSqlcGeneratedFilesHaveAQuery fecha a lacuna do `sqlc diff` do CI: ele
// compara o que geraria com o que existe, mas não vê o .sql.go que sobrou
// depois que a query foi apagada ou renomeada.
func TestSqlcGeneratedFilesHaveAQuery(t *testing.T) {
	gerados := nomesBase(t, "db/*.go")
	consultas := nomesBase(t, "queries/*.sql")
	if len(consultas) == 0 {
		t.Fatal("nenhuma query em queries/: o caminho do teste está errado")
	}
	if err := conferirSqlcSemOrfaos(gerados, consultas); err != nil {
		t.Fatal(err)
	}
}

func nomesBase(t *testing.T, padrao string) []string {
	t.Helper()
	caminhos, err := filepath.Glob(padrao)
	if err != nil {
		t.Fatal(err)
	}
	nomes := make([]string, 0, len(caminhos))
	for _, c := range caminhos {
		nomes = append(nomes, filepath.Base(c))
	}
	return nomes
}
