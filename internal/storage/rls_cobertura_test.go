package storage_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// tabelasSemRLS são as únicas tabelas de public que dispensam a RLS forçada. A
// chave é o nome exato em pg_class, e o valor é o motivo. Sem motivo escrito,
// a tabela não entra: ela recebe ENABLE, FORCE e política na própria migration.
var tabelasSemRLS = map[string]string{
	"goose_db_version":    "é do goose e não guarda dado de usuário; ele a lê e grava sem escopo a cada boot",
	"users":               "D7 do Plano 2: o login a consulta por e-mail antes de existir usuário na transação",
	"sessions":            "D7 do Plano 2: login e refresh a consultam por e-mail e por hash, antes de existir usuário na transação",
	"totp_recovery_codes": "as transações de login e 2FA não definem app.user_id; as queries filtram por user_id e a coluna guarda só hash",
	"audit_log":           "D7 do Plano 2: user_id é anulável, a linha sobrevive à conta apagada, e a retenção apaga as de todos",
}

// tabelaParticionadaConhecida impede a varredura de perder o relkind p em
// silêncio: sem ela, trocar o filtro por só tabelas comuns deixaria o teste
// verde e cego para qualquer tabela particionada nova.
const tabelaParticionadaConhecida = "checks"

// Sem FORCE, o papel da aplicação, que é dono das tabelas, ignora toda a RLS.
// Sem ENABLE, o FORCE não faz nada. Sem política, a tabela nega tudo, o que só
// é coerente com uma tabela que ninguém usa. Um teste de isolamento escrito à
// mão só protege a tabela que alguém lembrou de testar; este cobre a próxima.
//
// As partições ficam fora de propósito: RLS não se herda e elas não têm a
// própria, porque a política vale para consultas pela tabela-mãe, e esta é a
// tabela verificada aqui. A aplicação nunca nomeia uma partição.
func TestEveryTableForcesRLSWithAPolicy(t *testing.T) {
	pool := testdb.New(t)
	tabelas := lerTabelasDaAplicacao(t, pool)

	for _, tabela := range tabelas {
		faltas := tabela.protecoesAusentes()
		_, excecao := tabelasSemRLS[tabela.nome]
		if !excecao && len(faltas) > 0 {
			t.Errorf("tabela %s sem %s: corrija na migration ou declare o motivo em tabelasSemRLS",
				tabela.nome, strings.Join(faltas, ", "))
		}
		if excecao && len(faltas) == 0 {
			t.Errorf("tabela %s já tem RLS forçada e política: tire-a de tabelasSemRLS", tabela.nome)
		}
	}

	for nome := range tabelasSemRLS {
		if !slices.ContainsFunc(tabelas, func(tabela tabelaDaAplicacao) bool { return tabela.nome == nome }) {
			t.Errorf("tabelasSemRLS cita %s, que não existe em public: tire o nome morto", nome)
		}
	}

	if !slices.ContainsFunc(tabelas, func(tabela tabelaDaAplicacao) bool {
		return tabela.nome == tabelaParticionadaConhecida && tabela.relkind == "p"
	}) {
		t.Errorf("a varredura não viu %s como tabela particionada: ela não cobre o relkind p", tabelaParticionadaConhecida)
	}
}

type tabelaDaAplicacao struct {
	nome      string
	relkind   string
	ligada    bool
	forcada   bool
	politicas int
}

func (t tabelaDaAplicacao) protecoesAusentes() []string {
	var faltas []string
	if !t.ligada {
		faltas = append(faltas, "ENABLE ROW LEVEL SECURITY")
	}
	if !t.forcada {
		faltas = append(faltas, "FORCE ROW LEVEL SECURITY")
	}
	if t.politicas == 0 {
		faltas = append(faltas, "política")
	}
	return faltas
}

func lerTabelasDaAplicacao(t *testing.T, pool *pgxpool.Pool) []tabelaDaAplicacao {
	t.Helper()
	linhas, err := pool.Query(context.Background(),
		`SELECT c.relname, c.relkind::text, c.relrowsecurity, c.relforcerowsecurity,
		        (SELECT count(*) FROM pg_policy p WHERE p.polrelid = c.oid)
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = 'public'
		    AND c.relkind IN ('r', 'p')
		    AND NOT c.relispartition
		  ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("listar tabelas de public: %v", err)
	}
	defer linhas.Close()

	var tabelas []tabelaDaAplicacao
	for linhas.Next() {
		var tabela tabelaDaAplicacao
		if err := linhas.Scan(&tabela.nome, &tabela.relkind, &tabela.ligada, &tabela.forcada, &tabela.politicas); err != nil {
			t.Fatalf("ler linha do catálogo: %v", err)
		}
		tabelas = append(tabelas, tabela)
	}
	if err := linhas.Err(); err != nil {
		t.Fatalf("percorrer o catálogo: %v", err)
	}
	return tabelas
}
