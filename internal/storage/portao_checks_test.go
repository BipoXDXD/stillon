//go:build portao

package storage_test

// Portão de lock: mede o que a troca do CHECK de
// checks.error_kind (para aceitar 'content_mismatch') custa ao sondador com um
// volume de produção, nas duas formas possíveis da migration, e confere que a
// forma NOT VALID é aceita pelo Postgres em tabela particionada.
//
// Fica fora do `make test` e do CI pela build tag: carrega milhões de linhas.
//
//	PORTAO_LINHAS=17568000 go test -tags portao -run TestPortaoTrocaDoCheckEmChecks -v ./internal/storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

const (
	// 100 alvos × 2880 checks/dia (intervalo de 30 s) × 61 dias: o pior caso
	// de um dono no teto de alvos, com a retenção máxima de checks.
	linhasDoPortaoPadrao = 17_568_000
	alvosDoPortao        = 100
	diasDoPortao         = 61
	loteDaCarga          = 1_000_000
	// Uma em cada dez linhas é down, com error_kind variado.
	fracaoDown = 10

	intervaloDoSondador = 10 * time.Millisecond
	// Prazo de cada INSERT do sondador simulado: longo de propósito, para
	// medir a espera inteira em vez de cortá-la.
	prazoDoInsert = 10 * time.Minute

	restricaoErrorKind  = "checks_error_kind_check"
	listaAtualErrorKind = `'timeout', 'dns', 'tls', 'conn_refused', 'http_status', 'body_limit', 'blocked', 'unknown'`
)

var (
	// Os 61 dias terminam em 2026-11-09 e cobrem três partições mensais.
	inicioDosDados = time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	mesesDosDados  = []time.Time{
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}
	emParticaoAntiga = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	// O sondador simulado grava num instante fixo: com o relógio real, o
	// INSERT cairia fora das partições criadas aqui (não há DEFAULT) assim que
	// o mês virasse. Passado, porque a guarda recusa checked_at no futuro.
	emParticaoDoSondador = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	mesCriadoDepois      = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	emParticaoNova       = time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)
)

type medicaoDoPortao struct {
	forma            string
	linhas           int64
	alter            time.Duration
	maiorInsertAlter time.Duration
	validate         time.Duration
	maiorInsertValid time.Duration
	aceitacao        string
}

func TestPortaoTrocaDoCheckEmChecks(t *testing.T) {
	linhas := linhasDoPortao(t)
	for _, forma := range []struct {
		nome     string
		notValid bool
	}{
		{nome: "A", notValid: false},
		{nome: "B", notValid: true},
	} {
		t.Run("forma"+forma.nome, func(t *testing.T) {
			m := medirForma(t, linhas, forma.nome, forma.notValid)
			t.Logf("| forma | V | ALTER | maior INSERT no ALTER | VALIDATE | maior INSERT no VALIDATE | aceitação |")
			t.Logf("| %s | %d | %s | %s | %s | %s | %s |",
				m.forma, m.linhas, m.alter.Round(time.Millisecond), m.maiorInsertAlter.Round(time.Millisecond),
				duracaoOuTraco(m.validate), duracaoOuTraco(m.maiorInsertValid), m.aceitacao)
		})
	}
}

func linhasDoPortao(t *testing.T) int64 {
	t.Helper()
	v := os.Getenv("PORTAO_LINHAS")
	if v == "" {
		return linhasDoPortaoPadrao
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < alvosDoPortao {
		t.Fatalf("PORTAO_LINHAS=%q: precisa ser um inteiro >= %d", v, alvosDoPortao)
	}
	return n
}

func medirForma(t *testing.T, linhas int64, forma string, notValid bool) medicaoDoPortao {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	// O snapshot já vem com a migration da regra de conteúdo, que faz a troca
	// medida aqui: o portão parte do esquema anterior a ela. O cleanup de New
	// restaura o snapshot no fim.
	if err := storage.MigrateDownTo(ctx, testdb.DSN(t), versaoDaMigration(t, sufixoRegraDeConteudo)-1); err != nil {
		t.Fatalf("descer para antes da regra de conteúdo: %v", err)
	}
	admin := conectarAdminSemGatilhos(t)

	dono := novoUsuario(t, pool)
	alvos := make([]uuid.UUID, alvosDoPortao)
	for i := range alvos {
		alvos[i] = novoAlvo(t, pool, dono, agora)
	}
	// Pelo papel da aplicação, como em produção: ele é o dono das partições,
	// e o ALTER na mãe precisa ser dono de cada uma.
	q := db.New(pool)
	for _, mes := range mesesDosDados {
		if err := q.EnsureChecksPartition(ctx, mes); err != nil {
			t.Fatalf("criar partição %s: %v", mes.Format("2006-01"), err)
		}
	}
	carregarChecks(t, admin, linhas)
	confirmarNomeDaRestricao(t, pool)

	if err := inserirComoAdmin(ctx, admin, alvos[0], emParticaoAntiga, "content_mismatch"); !recusadoPeloErrorKind(err) {
		t.Fatalf("antes do ALTER, content_mismatch devia ser recusado por %s; veio %v", restricaoErrorKind, err)
	}

	m := medicaoDoPortao{forma: forma, linhas: linhas}
	m.alter, m.maiorInsertAlter = enquantoOSondadorGrava(t, pool, alvos[1], func() error {
		return trocarRestricao(t, notValid)
	})

	exigirAceitacao(t, admin, alvos[0], emParticaoAntiga, "partição antiga")
	if notValid {
		exigirNotValidEmCadaParticao(t, pool, len(mesesDosDados))
		if err := q.EnsureChecksPartition(ctx, mesCriadoDepois); err != nil {
			t.Fatalf("criar partição depois do ALTER: %v", err)
		}
		exigirRestricaoNaParticao(t, pool, "checks_2027_01")
		exigirAceitacao(t, admin, alvos[0], emParticaoNova, "partição criada depois")
		exigirRetencaoFuncionando(t, pool)

		m.validate, m.maiorInsertValid = enquantoOSondadorGrava(t, pool, alvos[1], func() error {
			return validarRestricao(t)
		})
	}
	m.aceitacao = "ok"
	return m
}

// conectarAdminSemGatilhos abre uma conexão de superusuário (sem RLS) com os
// gatilhos desligados: a carga não paga a guarda de relógio nem a FK por
// linha, e a aceitação pode gravar numa partição futura. CHECK não é gatilho
// e continua valendo.
func conectarAdminSemGatilhos(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatalf("conectar como admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	if _, err := admin.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	return admin
}

func carregarChecks(t *testing.T, admin *pgx.Conn, linhas int64) {
	t.Helper()
	ctx := context.Background()
	passo := float64(diasDoPortao*24*60*60) / float64(linhas)
	inicio := time.Now()
	for de := int64(0); de < linhas; de += loteDaCarga {
		ate := min(de+loteDaCarga, linhas) - 1
		if _, err := admin.Exec(ctx, `
			INSERT INTO checks (id, target_id, checked_at, status, latency_ms, status_code, error_kind)
			SELECT gen_random_uuid(),
			       a.ids[g % $4 + 1],
			       $3::timestamptz + make_interval(secs => g * $5::float8),
			       CASE WHEN g % $6 = 0 THEN 'down' ELSE 'up' END,
			       (g % 1000)::int,
			       CASE WHEN g % $6 = 0 THEN NULL ELSE 200 END,
			       CASE WHEN g % $6 = 0 THEN (ARRAY[`+listaAtualErrorKind+`])[(g / $6) % 8 + 1] END
			  FROM generate_series($1::bigint, $2::bigint) AS g,
			       (SELECT array_agg(id ORDER BY id) AS ids FROM targets) AS a`,
			de, ate, inicioDosDados, alvosDoPortao, passo, fracaoDown); err != nil {
			t.Fatalf("carregar checks [%d, %d]: %v", de, ate, err)
		}
	}
	if _, err := admin.Exec(ctx, `ANALYZE checks`); err != nil {
		t.Fatal(err)
	}
	var estimadas int64
	if err := admin.QueryRow(ctx, `SELECT sum(c.reltuples)::bigint FROM pg_class c
		JOIN pg_inherits i ON i.inhrelid = c.oid WHERE i.inhparent = 'checks'::regclass`).Scan(&estimadas); err != nil {
		t.Fatal(err)
	}
	t.Logf("carga: %d linhas em %s (reltuples após ANALYZE: %d)", linhas, time.Since(inicio).Round(time.Second), estimadas)
}

func confirmarNomeDaRestricao(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var definicao string
	err := pool.QueryRow(context.Background(), `SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE conrelid = 'checks'::regclass AND contype = 'c' AND conname = $1`, restricaoErrorKind).Scan(&definicao)
	if err != nil {
		t.Fatalf("a mãe checks não tem o CHECK %s: %v", restricaoErrorKind, err)
	}
	t.Logf("definição atual: %s", definicao)
}

// enquantoOSondadorGrava roda op com uma goroutine gravando um check a cada
// 10 ms pelo mesmo caminho do sondador, e devolve a duração de op e a maior
// latência de um INSERT na janela. Um INSERT que falha reprova o portão: a
// latência de uma gravação que não aconteceu não mede o sondador.
func enquantoOSondadorGrava(t *testing.T, pool *pgxpool.Pool, alvo uuid.UUID, op func() error) (time.Duration, time.Duration) {
	t.Helper()
	parar := make(chan struct{})
	var (
		wg     sync.WaitGroup
		maior  time.Duration
		falhas int
		gravou int
	)
	wg.Go(func() {
		passo := time.NewTicker(intervaloDoSondador)
		defer passo.Stop()
		for {
			select {
			case <-parar:
				return
			case <-passo.C:
			}
			lat, err := gravarComoSondador(pool, alvo)
			maior = max(maior, lat)
			gravou++
			if err != nil {
				falhas++
				t.Logf("INSERT do sondador falhou depois de %s: %v", lat.Round(time.Millisecond), err)
			}
		}
	})
	// Algumas gravações antes, para a medição incluir a fila que se forma
	// atrás da trava, e não só o que chega depois.
	time.Sleep(20 * intervaloDoSondador)
	inicio := time.Now()
	err := op()
	duracao := time.Since(inicio)
	time.Sleep(20 * intervaloDoSondador)
	close(parar)
	wg.Wait()
	if err != nil {
		t.Fatalf("operação medida: %v", err)
	}
	if gravou == 0 {
		t.Fatal("o sondador simulado não gravou nada na janela")
	}
	if falhas > 0 {
		t.Fatalf("%d de %d INSERTs do sondador simulado falharam na janela", falhas, gravou)
	}
	return duracao, maior
}

func gravarComoSondador(pool *pgxpool.Pool, alvo uuid.UUID) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), prazoDoInsert)
	defer cancel()
	code := int32(200)
	inicio := time.Now()
	err := storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: uuid.Must(uuid.NewV7()), TargetID: alvo, CheckedAt: emParticaoDoSondador,
			Status: "up", LatencyMS: 12, StatusCode: &code,
		})
	})
	return time.Since(inicio), err
}

// trocarRestricao é o trecho da migration, numa conexão do papel da
// aplicação sem o statement_timeout do pool, como o goose roda no boot.
func trocarRestricao(t *testing.T, notValid bool) error {
	t.Helper()
	sufixo := ""
	if notValid {
		sufixo = " NOT VALID"
	}
	return naTransacaoDaMigration(t, `ALTER TABLE checks DROP CONSTRAINT `+restricaoErrorKind+
		`, ADD CONSTRAINT `+restricaoErrorKind+` CHECK (error_kind IN (`+listaAtualErrorKind+`, 'content_mismatch'))`+sufixo)
}

func validarRestricao(t *testing.T) error {
	t.Helper()
	return naTransacaoDaMigration(t, `ALTER TABLE checks VALIDATE CONSTRAINT `+restricaoErrorKind)
}

func naTransacaoDaMigration(t *testing.T, sql string) error {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, testdb.DSN(t))
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, sql)
		return err
	})
}

func inserirComoAdmin(ctx context.Context, admin *pgx.Conn, alvo uuid.UUID, em time.Time, errorKind string) error {
	_, err := admin.Exec(ctx, `INSERT INTO checks (id, target_id, checked_at, status, latency_ms, error_kind)
		VALUES ($1, $2, $3, 'down', 0, $4)`, uuid.Must(uuid.NewV7()), alvo, em, errorKind)
	return err
}

func recusadoPeloErrorKind(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.ConstraintName == restricaoErrorKind
}

// exigirAceitacao: o valor novo entra e um valor fora da lista continua
// recusado, pela própria restrição (e não por outra que também dê 23514).
func exigirAceitacao(t *testing.T, admin *pgx.Conn, alvo uuid.UUID, em time.Time, onde string) {
	t.Helper()
	ctx := context.Background()
	if err := inserirComoAdmin(ctx, admin, alvo, em, "content_mismatch"); err != nil {
		t.Fatalf("%s: content_mismatch devia entrar depois do ALTER: %v", onde, err)
	}
	if err := inserirComoAdmin(ctx, admin, alvo, em, "xyz"); !recusadoPeloErrorKind(err) {
		t.Fatalf("%s: 'xyz' devia ser recusado por %s (23514); veio %v", onde, restricaoErrorKind, err)
	}
}

func exigirNotValidEmCadaParticao(t *testing.T, pool *pgxpool.Pool, particoes int) {
	t.Helper()
	var naoValidadas int
	var maeValidada bool
	err := pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM pg_constraint
		         WHERE conname = $1 AND NOT convalidated
		           AND conrelid IN (SELECT inhrelid FROM pg_inherits WHERE inhparent = 'checks'::regclass)),
		       (SELECT convalidated FROM pg_constraint WHERE conname = $1 AND conrelid = 'checks'::regclass)`,
		restricaoErrorKind).Scan(&naoValidadas, &maeValidada)
	if err != nil {
		t.Fatal(err)
	}
	if naoValidadas != particoes || maeValidada {
		t.Fatalf("NOT VALID: %d de %d partições com a restrição não validada, mãe validada = %v",
			naoValidadas, particoes, maeValidada)
	}
}

func exigirRestricaoNaParticao(t *testing.T, pool *pgxpool.Pool, particao string) {
	t.Helper()
	var tem bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_constraint
		WHERE conname = $1 AND conrelid = to_regclass($2))`, restricaoErrorKind, particao).Scan(&tem); err != nil {
		t.Fatal(err)
	}
	if !tem {
		t.Fatalf("a partição %s, criada depois do ALTER, não tem %s", particao, restricaoErrorKind)
	}
}

// exigirRetencaoFuncionando apaga a partição de setembro numa transação
// desfeita: prova que a retenção convive com a restrição NOT VALID sem tirar
// linhas da medição do VALIDATE.
func exigirRetencaoFuncionando(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var apagadas int
	if err := tx.QueryRow(ctx, `SELECT drop_checks_partitions_before($1)`, mesesDosDados[1]).Scan(&apagadas); err != nil {
		t.Fatalf("drop_checks_partitions_before com a restrição NOT VALID: %v", err)
	}
	if apagadas != 1 {
		t.Fatalf("drop_checks_partitions_before apagou %d partições; esperava 1 (setembro)", apagadas)
	}
}

func duracaoOuTraco(d time.Duration) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprint(d.Round(time.Millisecond))
}
