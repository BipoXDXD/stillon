package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A migration que estende a RLS de checks às partições, achada pelo nome: o
// número é o próximo livre na hora do merge.
const sufixoRLSParticoesChecks = "rls_particoes_checks"

// particaoDoMesDeAgora é a partição mensal que recebe os checks gravados em agora.
const particaoDoMesDeAgora = "checks_2026_09"

// Uma partição criada depois da migration já nasce com a RLS da mãe: quem não
// enxerga o alvo não enxerga os checks dele nem consultando a partição pelo
// nome. O sondador continua gravando pela mãe, e a retenção continua apagando
// a partição inteira.
func TestNewChecksPartitionEnforcesTheParentPolicy(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	estranho, dono := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, agora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvo, agora)

	exigirParticaoProtegida(t, pool, particaoDoMesDeAgora)
	if n := contarNaParticao(t, pool, estranho, particaoDoMesDeAgora); n != 0 {
		t.Errorf("outro usuário lê %d checks pela partição, queria 0", n)
	}
	if n := contarNaParticao(t, pool, dono, particaoDoMesDeAgora); n != 1 {
		t.Errorf("o dono lê %d checks pela partição, queria 1: a política barrou demais", n)
	}

	// O DROP da retenção não passa por RLS: a partição protegida sai igual.
	var apagadas int
	if err := pool.QueryRow(ctx, `SELECT drop_checks_partitions_before($1)`,
		agora.AddDate(0, 2, 0)).Scan(&apagadas); err != nil {
		t.Fatalf("retenção com a partição protegida: %v", err)
	}
	if apagadas != 1 {
		t.Errorf("retenção apagou %d partições, queria 1", apagadas)
	}
}

// A migration protege também as partições que já existiam antes dela, e o
// down devolve a função anterior e tira a RLS das partições.
func TestChecksPartitionRLSMigrationCoversExistingPartitionsAndGoesDown(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoRLSParticoesChecks)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao-1); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	estranho, dono := novoUsuario(t, pool), novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, agora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvo, agora)

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("up da %d: %v", versao, err)
	}
	exigirParticaoProtegida(t, pool, particaoDoMesDeAgora)
	if n := contarNaParticao(t, pool, estranho, particaoDoMesDeAgora); n != 0 {
		t.Errorf("partição anterior à migration: outro usuário lê %d checks, queria 0", n)
	}
	novoCheck(t, pool, alvo, agora.Add(1))

	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}
	if _, err := pool.Exec(ctx, `SELECT ensure_checks_partition($1::date)`, agora.AddDate(0, 1, 0)); err != nil {
		t.Fatalf("função anterior depois do down: %v", err)
	}
	for _, particao := range []string{particaoDoMesDeAgora, "checks_2026_10"} {
		if ligada, forcada, politicas := lerProtecaoDaParticao(t, pool, particao); ligada || forcada || politicas != "" {
			t.Errorf("depois do down, %s: ENABLE=%v FORCE=%v políticas=%q; queria tudo desligado",
				particao, ligada, forcada, politicas)
		}
	}

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo a %d: %v", versao, err)
	}
	exigirParticaoProtegida(t, pool, "checks_2026_10")
}

func exigirParticaoProtegida(t *testing.T, pool *pgxpool.Pool, particao string) {
	t.Helper()
	ligada, forcada, politicas := lerProtecaoDaParticao(t, pool, particao)
	if !ligada || !forcada {
		t.Errorf("partição %s: ENABLE=%v FORCE=%v; queria true e true", particao, ligada, forcada)
	}
	if mae := politicasDe(t, pool, "checks"); politicas == "" || politicas != mae {
		t.Errorf("partição %s com as políticas %q; queria as da mãe, %q", particao, politicas, mae)
	}
}

// lerProtecaoDaParticao devolve as políticas da partição como texto, para
// comparar com as da mãe: as migrations seguintes podem trocar as políticas, e
// partição e mãe têm de andar juntas.
func lerProtecaoDaParticao(t *testing.T, pool *pgxpool.Pool, particao string) (ligada, forcada bool, politicas string) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT c.relrowsecurity, c.relforcerowsecurity
		   FROM pg_class c
		  WHERE c.relname = $1 AND c.relispartition`, particao).Scan(&ligada, &forcada)
	if err != nil {
		t.Fatalf("ler a partição %s no catálogo: %v", particao, err)
	}
	return ligada, forcada, politicasDe(t, pool, particao)
}

// politicasDe descreve as políticas da tabela, uma por linha e em ordem de
// nome: nome, comando, permissiva ou restritiva, USING e WITH CHECK. O nome da
// própria tabela nas expressões vira <tabela>, porque a da partição se refere
// a checks_AAAA_MM e a da mãe a checks; comparar só nome e comando deixaria
// passar uma partição de política afrouxada.
func politicasDe(t *testing.T, pool *pgxpool.Pool, tabela string) string {
	t.Helper()
	var politicas string
	err := pool.QueryRow(context.Background(),
		`SELECT coalesce(string_agg(
		            concat_ws(' | ', p.polname, p.polcmd::text, p.polpermissive::text,
		                      replace(coalesce(pg_get_expr(p.polqual, p.polrelid), '-'),
		                              quote_ident(c.relname) || '.', '<tabela>.'),
		                      replace(coalesce(pg_get_expr(p.polwithcheck, p.polrelid), '-'),
		                              quote_ident(c.relname) || '.', '<tabela>.')),
		            E'\n' ORDER BY p.polname), '')
		   FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid
		  WHERE c.relname = $1`, tabela).Scan(&politicas)
	if err != nil {
		t.Fatalf("ler as políticas de %s: %v", tabela, err)
	}
	return politicas
}

// contarNaParticao conta os checks consultando a partição pelo nome, sob o
// escopo do usuário: o caminho que a política da mãe não cobre.
func contarNaParticao(t *testing.T, pool *pgxpool.Pool, usuario uuid.UUID, particao string) int {
	t.Helper()
	var n int
	err := emEscopo(t, pool, "app.user_id", usuario.String(), func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			`SELECT count(*) FROM `+pgx.Identifier{particao}.Sanitize()).Scan(&n)
	})
	if err != nil {
		t.Fatalf("contar na partição %s: %v", particao, err)
	}
	return n
}
