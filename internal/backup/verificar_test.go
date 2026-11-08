package backup

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// blocoDeVerificacao é o DO $$ de verificar.sql. O cabeçalho com \set e as
// variáveis é do psql; aqui elas entram por set_config, na mesma sessão.
func blocoDeVerificacao(t *testing.T) string {
	t.Helper()
	texto, err := os.ReadFile("../../deploy/backup/verificar.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, resto, ok := strings.Cut(string(texto), "DO $$")
	if !ok {
		t.Fatal("verificar.sql sem o bloco DO")
	}
	return "DO $$" + resto
}

const (
	semRLS = `DO $$ DECLARE t regclass; BEGIN
		FOR t IN SELECT c.oid::regclass FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		          WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND c.relrowsecurity LOOP
			EXECUTE format('ALTER TABLE %s DISABLE ROW LEVEL SECURITY', t);
		END LOOP; END $$`
	semParticoes = `DO $$ DECLARE p regclass; BEGIN
		FOR p IN SELECT inhrelid::regclass FROM pg_inherits WHERE inhparent = 'public.checks'::regclass LOOP
			EXECUTE format('DROP TABLE %s', p);
		END LOOP; END $$`
)

// O banco é o das migrations de verdade, com superusuário, como no restauro.
// Cada cópia começa boa e leva um defeito; a checagem certa tem de recusar.
func TestVerificarRefusesEachDefectOfTheRestoredCopy(t *testing.T) {
	bloco := blocoDeVerificacao(t)
	for _, tc := range []struct {
		nome            string
		defeito         string
		semDados        bool
		usuarios, alvos string
		origemAFrente   int
		erro            string
	}{
		{nome: "cópia igual à origem", usuarios: "1", alvos: "1"},
		{nome: "tabela de outro dono", defeito: `CREATE TABLE public.alheia (id int)`, usuarios: "1", alvos: "1", erro: "tabelas sem o dono stillon_app: alheia"},
		{nome: "RLS sem FORCE", defeito: `ALTER TABLE targets NO FORCE ROW LEVEL SECURITY`, usuarios: "1", alvos: "1", erro: "tabelas com RLS sem FORCE: targets"},
		{nome: "nenhuma política", defeito: semRLS, usuarios: "1", alvos: "1", erro: "nenhuma tabela com row-level security"},
		{nome: "schema atrás da origem", origemAFrente: 1, usuarios: "1", alvos: "1", erro: "versão do schema restaurado"},
		{nome: "schema à frente da origem", origemAFrente: -1, usuarios: "1", alvos: "1", erro: "versão do schema restaurado"},
		{nome: "goose_db_version vazio", defeito: `DELETE FROM goose_db_version`, usuarios: "1", alvos: "1", erro: "versão do schema restaurado"},
		{nome: "RLS sem FORCE na tabela particionada", defeito: `ALTER TABLE checks NO FORCE ROW LEVEL SECURITY`, usuarios: "1", alvos: "1", erro: "tabelas com RLS sem FORCE: checks"},
		{nome: "banco sem usuário", semDados: true, usuarios: "0", alvos: "0", erro: "users restaurado = 0"},
		{nome: "menos usuários que a origem", usuarios: "2", alvos: "1", erro: "users restaurado = 1, origem = 2"},
		{nome: "menos alvos que a origem", usuarios: "1", alvos: "2", erro: "targets restaurado = 1, origem = 2"},
		{nome: "checks sem partição", defeito: semParticoes, usuarios: "1", alvos: "1", erro: "checks restaurado sem partições"},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			pool := testdb.New(t)
			ctx := context.Background()
			agora := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
			if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
				t.Fatal(err)
			}
			admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close(ctx)
			if !tc.semDados {
				semearCopia(t, ctx, admin, agora, 1, 1)
			}
			versao := versaoDoSchema(t, ctx, admin)
			if tc.defeito != "" {
				if _, err := admin.Exec(ctx, tc.defeito); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := admin.Exec(ctx, `SELECT set_config('verificar.versao', $1, false), set_config('verificar.usuarios', $2, false), set_config('verificar.alvos', $3, false)`,
				strconv.FormatInt(versao+int64(tc.origemAFrente), 10), tc.usuarios, tc.alvos); err != nil {
				t.Fatal(err)
			}

			_, err = admin.Exec(ctx, bloco)

			if tc.erro == "" {
				if err != nil {
					t.Fatalf("a cópia boa foi recusada: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.erro) {
				t.Fatalf("erro = %v, queria %q", err, tc.erro)
			}
		})
	}
}

// O arquivo inteiro pelo psql, com a mesma linha de comando do backup.sh, que
// não passa ON_ERROR_STOP: quem transforma o RAISE em saída 3 é o próprio
// arquivo. Usuários e alvos têm números diferentes para acusar variável trocada.
func TestVerificarFileThroughPsql(t *testing.T) {
	script, err := os.ReadFile("../../deploy/backup/verificar.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		nome             string
		linhasU, linhasA int
		usuarios, alvos  string
		codigo           int
		saida            string
	}{
		{nome: "mais alvos que usuários", linhasU: 1, linhasA: 3, usuarios: "1", alvos: "3"},
		{nome: "mais usuários que alvos", linhasU: 3, linhasA: 1, usuarios: "3", alvos: "1"},
		{nome: "defeito sai com código 3", linhasU: 1, linhasA: 1, usuarios: "2", alvos: "1", codigo: 3, saida: "users restaurado = 1, origem = 2"},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			pool := testdb.New(t)
			ctx := context.Background()
			agora := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
			if err := storage.EnsureChecksPartitions(ctx, pool, agora); err != nil {
				t.Fatal(err)
			}
			admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close(ctx)
			semearCopia(t, ctx, admin, agora, tc.linhasU, tc.linhasA)
			versao := strconv.FormatInt(versaoDoSchema(t, ctx, admin), 10)

			codigo, saida := testdb.Psql(t, script, "-v", "versao="+versao, "-v", "usuarios="+tc.usuarios, "-v", "alvos="+tc.alvos)

			if codigo != tc.codigo || !strings.Contains(saida, tc.saida) {
				t.Fatalf("psql saiu com %d e %q; queria %d e %q", codigo, saida, tc.codigo, tc.saida)
			}
		})
	}
}

func versaoDoSchema(t *testing.T, ctx context.Context, admin *pgx.Conn) int64 {
	t.Helper()
	var versao int64
	if err := admin.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version`).Scan(&versao); err != nil {
		t.Fatal(err)
	}
	return versao
}

// semearCopia põe as linhas dadas; os alvos são todos do primeiro usuário.
func semearCopia(t *testing.T, ctx context.Context, admin *pgx.Conn, agora time.Time, usuarios, alvos int) {
	t.Helper()
	var dono uuid.UUID
	for range usuarios {
		dono = uuid.Must(uuid.NewV7())
		if _, err := admin.Exec(ctx, `INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, dono, fmt.Sprintf("%s@exemplo.com", dono)); err != nil {
			t.Fatal(err)
		}
	}
	for range alvos {
		if _, err := admin.Exec(ctx, `INSERT INTO targets (id, user_id, name, url, method, interval_sec, timeout_ms, expected_status, enabled, next_run_at, created_at)
			VALUES ($1, $2, 'alvo', 'https://exemplo.com', 'GET', 60, 5000, 200, true, $3, $3)`, uuid.Must(uuid.NewV7()), dono, agora); err != nil {
			t.Fatal(err)
		}
	}
}
