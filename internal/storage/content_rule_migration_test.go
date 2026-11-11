package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const sufixoRegraDeConteudo = "targets_content_rule"

// regraDeConteudo é a regra crua de um alvo, NULL onde o ponteiro é nil.
type regraDeConteudo struct {
	modo, valor, ponteiro *string
}

// O banco recusa a regra pela metade: modo sem valor, ponteiro fora do
// json_equals, valor ou ponteiro fora do tamanho, regra em alvo HEAD. A
// aplicação valida antes, mas um INSERT por fora dela não grava um alvo que o
// sondador não saberia avaliar.
func TestContentRuleChecksAcceptOnlyWholeRules(t *testing.T) {
	pool := testdb.New(t)
	dono := novoUsuario(t, pool)

	for _, tc := range []struct {
		nome   string
		metodo string
		regra  regraDeConteudo
		recusa bool
	}{
		{"sem_regra", "GET", regraDeConteudo{}, false},
		{"contains_com_valor", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto("ok"), nil}, false},
		{"json_equals_com_valor_e_ponteiro", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/a")}, false},
		{"modo_sem_valor", "GET", regraDeConteudo{ptrTexto("contains"), nil, nil}, true},
		{"valor_sem_modo", "GET", regraDeConteudo{nil, ptrTexto("ok"), nil}, true},
		{"contains_com_ponteiro", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto("ok"), ptrTexto("/a")}, true},
		{"json_equals_sem_ponteiro", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), nil}, true},
		{"ponteiro_sem_barra", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("a")}, true},
		{"not_contains_com_valor", "GET", regraDeConteudo{ptrTexto("not_contains"), ptrTexto("erro"), nil}, false},
		{"modo_desconhecido", "GET", regraDeConteudo{ptrTexto("regex"), ptrTexto("ok"), nil}, true},
		{"valor_de_256", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto(strings.Repeat("a", 256)), nil}, false},
		{"valor_de_257", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto(strings.Repeat("a", 257)), nil}, true},
		{"valor_vazio", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto(""), nil}, true},
		{"ponteiro_de_256", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/" + strings.Repeat("a", 255))}, false},
		{"ponteiro_de_257", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/" + strings.Repeat("a", 256))}, true},
		{"ponteiro_vazio", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("")}, true},
		{"regra_em_head", "HEAD", regraDeConteudo{ptrTexto("contains"), ptrTexto("ok"), nil}, true},
		{"ponteiro_com_escapes_validos", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/a~0b/c~1d/")}, false},
		{"ponteiro_com_til_2", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/a~2")}, true},
		{"ponteiro_com_til_no_fim", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/a~")}, true},
		{"ponteiro_com_controle_01", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/a\x01b")}, true},
		{"ponteiro_com_controle_c1", "GET", regraDeConteudo{ptrTexto("json_equals"), ptrTexto(`"ok"`), ptrTexto("/a\u0085b")}, true},
		{"valor_com_acento", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto("ação"), nil}, false},
		{"valor_com_controle_01", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto("a\x01b"), nil}, true},
		{"valor_com_tab", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto("a\tb"), nil}, true},
		{"valor_com_del", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto("a\x7fb"), nil}, true},
		{"valor_com_controle_c1", "GET", regraDeConteudo{ptrTexto("contains"), ptrTexto("a\u0085b"), nil}, true},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			err := inserirAlvoComRegra(t, pool, dono, tc.metodo, tc.regra)
			switch {
			case tc.recusa && !violaCheck(err):
				t.Errorf("regra inválida: erro = %v, queria check_violation (23514)", err)
			case !tc.recusa && err != nil:
				t.Errorf("regra válida recusada: %v", err)
			}
		})
	}
}

// content_mismatch e content_encoding são motivos de check em queda e causas
// de incidente; o enum continua fechado para o resto.
func TestContentMismatchIsAcceptedInChecksAndIncidents(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	particaoDeAgora(t, pool)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)

	for _, motivo := range []string{"content_mismatch", "content_encoding"} {
		// Um alvo por motivo: só cabe um incidente aberto por alvo.
		alvoDoMotivo := novoAlvo(t, pool, dono, agora)
		if err := gravarCheckDown(ctx, pool, alvoDoMotivo, motivo); err != nil {
			t.Errorf("check %s recusado: %v", motivo, err)
		}
		if _, err := abrirIncidenteCom(ctx, pool, alvoDoMotivo, motivo); err != nil {
			t.Errorf("incidente %s recusado: %v", motivo, err)
		}
	}
	if err := gravarCheckDown(ctx, pool, alvo, "xyz"); !violaCheck(err) {
		t.Errorf("error_kind 'xyz': erro = %v, queria check_violation (23514)", err)
	}
}

// Reverter o deploy desce a migration: o motivo novo vira 'unknown' (o binário
// anterior não conhece outro), as colunas e as constraints somem, e o enum
// antigo volta a valer. Subir de novo funciona.
func TestContentRuleDownConvertsAndRestores(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoRegraDeConteudo)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	particaoDeAgora(t, pool)
	dono := novoUsuario(t, pool)
	if err := inserirAlvoComRegra(t, pool, dono, "GET",
		regraDeConteudo{ptrTexto("contains"), ptrTexto("ok"), nil}); err != nil {
		t.Fatalf("alvo com regra: %v", err)
	}
	alvo := novoAlvo(t, pool, dono, agora)
	// Sem incidente aberto: recebe a tentativa de content_mismatch depois do
	// down, que no outro alvo bateria no índice do incidente aberto.
	alvoSemIncidente := novoAlvo(t, pool, dono, agora)
	checkID := novoID()
	if err := gravarCheckDownComID(ctx, pool, checkID, alvo, "content_mismatch"); err != nil {
		t.Fatalf("check content_mismatch: %v", err)
	}
	incidente, err := abrirIncidenteCom(ctx, pool, alvo, "content_mismatch")
	if err != nil {
		t.Fatalf("incidente content_mismatch: %v", err)
	}
	alvoDoEncoding := novoAlvo(t, pool, dono, agora)
	checkDoEncoding := novoID()
	if err := gravarCheckDownComID(ctx, pool, checkDoEncoding, alvoDoEncoding, "content_encoding"); err != nil {
		t.Fatalf("check content_encoding: %v", err)
	}
	incidenteDoEncoding, err := abrirIncidenteCom(ctx, pool, alvoDoEncoding, "content_encoding")
	if err != nil {
		t.Fatalf("incidente content_encoding: %v", err)
	}

	if err := storage.MigrateDownTo(ctx, dsn, versao-1); err != nil {
		t.Fatalf("down da %d: %v", versao, err)
	}

	var motivo, causa, motivoDoEncoding, causaDoEncoding string
	err = emEscopo(t, pool, "app.scope", "prober", func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT error_kind FROM checks WHERE id = $1`, checkID).Scan(&motivo); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT error_kind FROM checks WHERE id = $1`, checkDoEncoding).Scan(&motivoDoEncoding); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT cause FROM incidents WHERE id = $1`, incidenteDoEncoding).Scan(&causaDoEncoding); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT cause FROM incidents WHERE id = $1`, incidente).Scan(&causa)
	})
	if err != nil {
		t.Fatalf("ler check e incidente depois do down: %v", err)
	}
	if motivo != "unknown" || causa != "unknown" || motivoDoEncoding != "unknown" || causaDoEncoding != "unknown" {
		t.Errorf("depois do down: checks %q e %q, incidentes %q e %q, queria todos 'unknown'",
			motivo, motivoDoEncoding, causa, causaDoEncoding)
	}

	var colunas, restricoes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'targets' AND column_name IN ('content_mode', 'content_value', 'content_json_pointer')`).
		Scan(&colunas); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
		WHERE conname IN ('targets_content_rule_shape', 'targets_content_rule_not_head')`).Scan(&restricoes); err != nil {
		t.Fatal(err)
	}
	if colunas != 0 || restricoes != 0 {
		t.Errorf("depois do down: %d colunas e %d constraints da regra, queria 0 e 0", colunas, restricoes)
	}
	if err := gravarCheckDown(ctx, pool, alvo, "content_mismatch"); !violaCheck(err) {
		t.Errorf("content_mismatch depois do down: erro = %v, queria check_violation (23514)", err)
	}
	if _, err := abrirIncidenteCom(ctx, pool, alvoSemIncidente, "content_mismatch"); !violaCheck(err) {
		t.Errorf("incidente content_mismatch depois do down: erro = %v, queria check_violation (23514)", err)
	}

	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("subir de novo a %d: %v", versao, err)
	}
}

// O piso de rollback é a imagem de antes desta migration: o boot dela, num
// banco que já tem a regra, é um no-op. O provider é montado como em
// storage.Migrate.
func TestFloorBinaryBootsOnContentRule(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	anterior, err := goose.NewProvider(goose.DialectPostgres, conn,
		migrationsAte(t, arquivoDaMigration(t, sufixoRegraDeConteudo)))
	if err != nil {
		t.Fatal(err)
	}
	aplicadas, err := anterior.Up(ctx)
	if err != nil {
		t.Fatalf("boot do binário anterior: %v", err)
	}
	if len(aplicadas) != 0 {
		t.Errorf("o binário anterior aplicou %d migrations", len(aplicadas))
	}
}

// A migration desiste entre os dois limites: o teto é folga sobre o
// lock_timeout de 5 s, e o piso fixa o Ruling contra um prazo curto, que
// derrubaria o boot com 55P03 a cada leitura longa.
const (
	pisoDaMigrationTravada = 4 * time.Second
	tetoDaMigrationTravada = 8 * time.Second
)

// Um sondador no meio de uma gravação segura as tabelas que a migration
// altera. A migration desiste com lock_timeout em vez de enfileirar todo o
// tráfego atrás do seu ACCESS EXCLUSIVE, e não deixa versão gravada; liberada
// a trava, o próximo boot a aplica.
func TestContentRuleMigrationGivesUpOnLockTimeout(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoRegraDeConteudo)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.MigrateUpTo(ctx, dsn, versao-1); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	particaoDeAgora(t, pool)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	antes := versaoDoBanco(t, pool)

	sondador, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sondador.Rollback(ctx)
	if _, err := sondador.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sondador.Exec(ctx, `INSERT INTO checks (id, target_id, checked_at, status, latency_ms, error_kind)
		VALUES ($1, $2, $3, 'down', 1, 'timeout')`, novoID(), alvo, agora); err != nil {
		t.Fatal(err)
	}

	// O prazo do contexto só existe para o teste não travar se o lock_timeout
	// sumir: nesse caso o erro é de contexto, não 55P03, e o teste falha.
	ctxMigracao, cancelar := context.WithTimeout(ctx, 30*time.Second)
	defer cancelar()
	inicio := time.Now()
	err = storage.MigrateUpTo(ctxMigracao, dsn, versao)
	gasto := time.Since(inicio)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("migration com a trava do sondador: erro = %v, queria lock_not_available (55P03)", err)
	}
	if gasto < pisoDaMigrationTravada || gasto >= tetoDaMigrationTravada {
		t.Errorf("a migration desistiu em %v, queria entre %v e %v", gasto, pisoDaMigrationTravada, tetoDaMigrationTravada)
	}
	if depois := versaoDoBanco(t, pool); depois != antes {
		t.Errorf("goose_db_version foi de %d para %d na migration que desistiu", antes, depois)
	}

	if err := sondador.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := storage.MigrateUpTo(ctx, dsn, versao); err != nil {
		t.Fatalf("migration depois de liberada a trava: %v", err)
	}
}

// O Down trava as tabelas na ordem do sondador, targets antes de checks. Um
// sondador que já segura o alvo (LockTargetProbeSpec) e grava o check depois
// só espera o Down; na ordem inversa, os dois se esperariam e o Postgres
// abortaria um deles com 40P01.
func TestContentRuleDownLocksTargetsBeforeChecks(t *testing.T) {
	ctx := context.Background()
	versao := versaoDaMigration(t, sufixoRegraDeConteudo)
	dsn := testdb.EmptyDatabase(t)
	if err := storage.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	particaoDeAgora(t, pool)
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)

	sondador, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sondador.Rollback(ctx)
	if _, err := sondador.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(sondador).LockTargetProbeSpec(ctx, alvo); err != nil {
		t.Fatal(err)
	}

	desceu := make(chan error, 1)
	go func() { desceu <- storage.MigrateDownTo(ctx, dsn, versao-1) }()
	if !testdb.EsperarBloqueados(t, pool, 1, desceu) {
		t.Fatalf("o Down não esperou a trava do sondador: %v", <-desceu)
	}

	if _, err := sondador.Exec(ctx, `INSERT INTO checks (id, target_id, checked_at, status, latency_ms, error_kind)
		VALUES ($1, $2, $3, 'down', 1, 'timeout')`, novoID(), alvo, agora); err != nil {
		t.Errorf("INSERT do sondador com o Down na fila: %v", err)
	}
	if err := sondador.Commit(ctx); err != nil {
		t.Errorf("commit do sondador: %v", err)
	}
	if err := <-desceu; err != nil {
		t.Errorf("Down com o sondador gravando: %v", err)
	}
}

// particaoDeAgora cria a partição de checks do mês de agora, que o banco
// migrado hoje não tem.
func particaoDeAgora(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := storage.EnsureChecksPartitions(context.Background(), pool, agora); err != nil {
		t.Fatal(err)
	}
}

func versaoDoBanco(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var v int64
	if err := pool.QueryRow(context.Background(),
		`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// inserirAlvoComRegra grava o alvo com SQL cru no escopo do dono: o
// InsertTarget gerado ainda não cita as colunas da regra.
func inserirAlvoComRegra(t *testing.T, pool *pgxpool.Pool, dono uuid.UUID, metodo string, r regraDeConteudo) error {
	t.Helper()
	return emEscopo(t, pool, "app.user_id", dono.String(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO targets
			(id, user_id, name, url, method, interval_sec, timeout_ms, expected_status, enabled,
			 next_run_at, created_at, content_mode, content_value, content_json_pointer)
			VALUES ($1, $2, 'alvo', 'https://exemplo.com', $3, 60, 5000, 200, true, $4, $4, $5, $6, $7)`,
			novoID(), dono, metodo, agora, r.modo, r.valor, r.ponteiro)
		return err
	})
}

func gravarCheckDown(ctx context.Context, pool *pgxpool.Pool, alvo uuid.UUID, motivo string) error {
	return gravarCheckDownComID(ctx, pool, novoID(), alvo, motivo)
}

func gravarCheckDownComID(ctx context.Context, pool *pgxpool.Pool, id, alvo uuid.UUID, motivo string) error {
	return storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: id, TargetID: alvo, CheckedAt: agora, Status: "down", LatencyMS: 1, ErrorKind: &motivo,
		})
	})
}

func abrirIncidenteCom(ctx context.Context, pool *pgxpool.Pool, alvo uuid.UUID, causa string) (uuid.UUID, error) {
	id := novoID()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.OpenIncident(ctx, db.OpenIncidentParams{ID: id, TargetID: alvo, StartedAt: agora, Cause: causa})
	})
	return id, err
}

func ptrTexto(s string) *string { return &s }
