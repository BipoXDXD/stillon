// Package testdb sobe um Postgres real para os testes de integração.
//
// A spec (§14) proíbe mock de banco: RLS, SKIP LOCKED e violação de
// constraint são invisíveis contra um mock, e são justamente o que precisa de
// teste. O contêiner sobe uma vez por execução de pacote; cada teste recebe o
// banco restaurado ao estado pós-migration via snapshot.
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/BipoXDXD/stillon/internal/storage"
)

const (
	dbName    = "stillon_test"
	adminUser = "stillon"
	adminPass = "stillon"

	// appUser é o papel com que a aplicação roda nos testes, e existe por
	// causa da RLS. O usuário do contêiner é superusuário, e superusuário
	// atravessa toda política de RLS, mesmo com FORCE. Com ele, um teste de
	// isolamento passaria pelo WHERE da query e nunca pela política, e
	// apagar a política deixaria a suíte verde. As migrations rodam como
	// appUser para que ele seja o DONO das tabelas, exatamente como em
	// produção (D8).
	appUser = "stillon_app"
	appPass = "stillon_app"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	adminDSN  string
	appDSN    string
	startErr  error
	vazios    atomic.Int64
)

// Cada pacote de go test é um processo, mas o Testcontainers deriva a sessão
// pelo pai compartilhado. Um Ryuk recriado por outro pacote pode então recolher
// nosso Postgres ainda em uso se a ligação ao Ryuk anterior falhou no handshake.
// Definir antes do primeiro acesso à configuração mantém provedor, labels e
// guardião na mesma sessão, exclusiva deste processo. O namespace herdado vira
// hash para não expor texto arbitrário nem exceder o limite do nome Docker.
func init() {
	namespace := sha256.Sum256([]byte(os.Getenv("TESTCONTAINERS_SESSION_ID")))
	var nonce [16]byte
	// No Go 1.26, Read preenche tudo ou encerra o processo se não houver entropia.
	_, _ = rand.Read(nonce[:])
	id := fmt.Sprintf("stillon-test-%x-%d-%x", namespace[:8], os.Getpid(), nonce)
	if err := os.Setenv("TESTCONTAINERS_SESSION_ID", id); err != nil {
		panic(fmt.Errorf("definir sessão do Postgres de teste: %w", err))
	}
}

// New devolve um pool ligado a um Postgres limpo e migrado, como o papel
// da aplicação.
//
// Pula com -short: quem não tem Docker disponível ainda consegue rodar os
// testes dos pacotes puros (crypto, safedial, password) com `make test-short`.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	iniciar(t)

	ctx := context.Background()
	pool, err := storage.NewPool(ctx, appDSN)
	if err != nil {
		t.Fatalf("conectar ao Postgres de teste: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// Restore precisa de zero conexões abertas no banco alvo, por isso
		// vem depois do Close.
		if err := container.Restore(ctx); err != nil {
			t.Fatalf("restaurar snapshot: %v", err)
		}
	})
	return pool
}

// DSN é a string de conexão do papel da aplicação, para quem precisa passar
// pelo caminho de boot (storage.Migrate, run) em vez de receber um pool
// pronto. Chame New antes no mesmo teste: é o cleanup de New que restaura o
// snapshot quando o teste termina.
func DSN(t *testing.T) string {
	t.Helper()
	iniciar(t)
	return appDSN
}

// LockTable trava table com ACCESS EXCLUSIVE numa transação que só acaba no
// fim do teste: toda consulta que a toque fica presa, como atrás de uma
// conexão meio-aberta ou de uma migration demorada. Serve para provar que um
// laço desiste no prazo, em vez de esperar para sempre. O papel da aplicação
// é dono das tabelas, e por isso pode travá-las.
func LockTable(t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transação que trava %s: %v", table, err)
	}
	// Registrado depois do cleanup de New: roda antes, e a trava sai antes
	// de o pool fechar e o snapshot voltar.
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil {
			t.Errorf("soltar a trava de %s: %v", table, err)
		}
	})
	// #nosec G201 -- table vem do próprio teste, sanitizada como identificador.
	if _, err := tx.Exec(ctx, fmt.Sprintf(`LOCK TABLE %s IN ACCESS EXCLUSIVE MODE`, pgx.Identifier{table}.Sanitize())); err != nil {
		t.Fatalf("travar %s: %v", table, err)
	}
}

// AdminDSN é a conexão como superusuário. Serve para provar que o boot
// RECUSA esse papel e para ler o banco "por fora" da RLS, como faria quem
// tivesse um dump.
func AdminDSN(t *testing.T) string {
	t.Helper()
	iniciar(t)
	return adminDSN
}

// EmptyDatabase cria um banco novo, vazio e NÃO migrado, cujo dono é o
// papel da aplicação, e o apaga no fim do teste. O teste de migrations
// concorrentes precisa dele, porque o banco de New já nasce migrado.
func EmptyDatabase(t *testing.T) string {
	t.Helper()
	iniciar(t)
	ctx := context.Background()

	// O nome cru vai na URL; o sanitizado, entre aspas, no SQL. Os dois saem
	// da MESMA leitura do contador: relê-lo com testes em paralelo devolveria
	// o número de outro teste.
	cru := fmt.Sprintf("stillon_vazio_%d", vazios.Add(1))
	nome := pgx.Identifier{cru}.Sanitize()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("conectar como admin: %v", err)
	}
	defer admin.Close(ctx)

	// #nosec G201 -- nome vem de pgx.Identifier.Sanitize sobre um contador; nada vem de fora.
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, nome, appUser)); err != nil {
		t.Fatalf("criar banco vazio: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), adminDSN)
		if err != nil {
			t.Errorf("conectar para apagar banco vazio: %v", err)
			return
		}
		defer c.Close(context.Background())
		// #nosec G201 -- mesmo identificador sanitizado acima.
		if _, err := c.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, nome)); err != nil {
			t.Errorf("apagar banco vazio: %v", err)
		}
	})

	u, err := url.Parse(appDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + cru
	return u.String()
}

func iniciar(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("precisa de Docker; pulado em -short")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatalf("subir Postgres de teste: %v", startErr)
	}
}

// imagemPadrao é a imagem de desenvolvimento e testes: a mesma de produção,
// por digest (D29 do Plano 7); a CI escolhe a versão por TEST_POSTGRES_IMAGE.
// O Dependabot não atualiza esta string: mantenha-a igual ao digest de
// deploy/vps/db/Dockerfile e atualize as duas juntas (e a matriz do ci.yml).
const imagemPadrao = "postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"

func imagem() string {
	if v := os.Getenv("TEST_POSTGRES_IMAGE"); v != "" {
		return v
	}
	return imagemPadrao
}

func start() {
	ctx := context.Background()

	container, startErr = tcpostgres.Run(ctx,
		imagem(),
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername(adminUser),
		tcpostgres.WithPassword(adminPass),
		tcpostgres.WithSQLDriver("pgx"), // exigido por Snapshot/Restore
		tcpostgres.BasicWaitStrategies(),
	)
	if startErr != nil {
		return
	}

	adminDSN, startErr = container.ConnectionString(ctx, "sslmode=disable")
	if startErr != nil {
		return
	}
	if startErr = criarPapelDaAplicacao(ctx); startErr != nil {
		return
	}

	u, err := url.Parse(adminDSN)
	if err != nil {
		startErr = err
		return
	}
	u.User = url.UserPassword(appUser, appPass)
	appDSN = u.String()

	// O snapshot é tirado com o schema já migrado e sem dados, então cada
	// teste começa exatamente daqui.
	if startErr = storage.Migrate(ctx, appDSN); startErr != nil {
		return
	}
	startErr = container.Snapshot(ctx)
}

func criarPapelDaAplicacao(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("conectar como admin: %w", err)
	}
	defer conn.Close(ctx)

	for _, q := range []string{
		`CREATE ROLE ` + appUser + ` LOGIN PASSWORD '` + appPass + `' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE`,
		// CREATE no banco: a migration 00001 instala citext, que é trusted
		// desde o PG13 e por isso só exige este privilégio.
		`GRANT CREATE ON DATABASE ` + dbName + ` TO ` + appUser,
		// Desde o PG15 o schema public não dá CREATE a PUBLIC.
		`GRANT CREATE ON SCHEMA public TO ` + appUser,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("preparar papel da aplicação: %w", err)
		}
	}
	return nil
}

// Terminate derruba o contêiner. Chame de um TestMain quando quiser
// encerramento explícito; o Ryuk do testcontainers já limpa sozinho depois.
func Terminate() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}
