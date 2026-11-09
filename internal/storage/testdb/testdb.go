// Package testdb sobe um Postgres real para os testes de integração.
//
// A spec (§14) proíbe mock de banco: RLS, SKIP LOCKED e violação de
// constraint são invisíveis contra um mock, e são justamente o que precisa de
// teste. O contêiner sobe uma vez por execução de pacote. Com New, cada teste
// recebe o banco compartilhado, restaurado ao estado pós-migration via
// snapshot (testes em série). Com NovoBanco, cada teste recebe um banco só
// dele, copiado de um modelo migrado, e pode rodar com t.Parallel().
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
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

	// modelo é a cópia do banco recém-migrado de onde NovoBanco tira cada
	// banco de teste. Ninguém se conecta a ele: CREATE DATABASE ... TEMPLATE
	// recusa um modelo com sessão aberta.
	modelo = "stillon_modelo"

	// bancoDeManutencao recebe as conexões de quem cria e apaga bancos.
	bancoDeManutencao = "postgres"

	// maxConexoes é o max_connections do contêiner. Cada banco de teste abre
	// um pool de até 10 conexões (storage.NewPool), e o go test roda até
	// GOMAXPROCS testes em paralelo: o padrão do Postgres, 100, acabaria em
	// máquinas com 10 núcleos ou mais. 400 cobre até 39.
	maxConexoes = "400"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	adminDSN  string
	appDSN    string
	startErr  error
	vazios    atomic.Int64
	bancos    atomic.Int64
	// dsnsPorTeste liga o teste que pediu NovoBanco às conexões do banco
	// dele, para DSN e AdminDSN apontarem para o mesmo banco que o pool.
	dsnsPorTeste sync.Map // *testing.T → dsnsDoBanco
)

type dsnsDoBanco struct{ app, admin string }

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

// intervaloDaConsulta é o passo do polling de EsperarBloqueados, não um prazo.
const intervaloDaConsulta = 10 * time.Millisecond

// EsperarBloqueados é a corrida entre dois eventos: n backends deste banco
// pararem numa trava (de linha, de tabela ou advisory) e a operação que devia
// parar terminar antes, o defeito que os testes de trava procuram. Devolve
// true no primeiro caso e false no segundo. Não tem prazo próprio, porque prazo
// de parede falha à toa com a máquina carregada. O limite é a morte do processo
// pelo -timeout do go test, que não cancela o t.Context().
//
// fim precisa de buffer: a espera olha len(fim), sem consumir, e o resultado
// continua lá para o teste.
func EsperarBloqueados[T any](t *testing.T, pool *pgxpool.Pool, n int, fim <-chan T) bool {
	t.Helper()
	if cap(fim) == 0 {
		t.Fatal("EsperarBloqueados precisa de um canal com buffer")
	}
	passo := time.NewTicker(intervaloDaConsulta)
	defer passo.Stop()
	for {
		var bloqueados int
		if err := pool.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&bloqueados); err != nil {
			t.Fatal(err)
		}
		if bloqueados >= n {
			return true
		}
		if len(fim) > 0 {
			return false
		}
		<-passo.C
	}
}

// DSN é a string de conexão do papel da aplicação, para quem precisa passar
// pelo caminho de boot (storage.Migrate, run) em vez de receber um pool
// pronto. Chame New antes no mesmo teste: é o cleanup de New que restaura o
// snapshot quando o teste termina.
//
// Num teste que pediu NovoBanco, é a do banco desse teste. A busca é pelo t
// exato: num subteste, que é outro *testing.T, a chamada cai no banco
// compartilhado. Passe o t de quem chamou NovoBanco.
func DSN(t *testing.T) string {
	t.Helper()
	iniciar(t)
	if d, ok := dsnsPorTeste.Load(t); ok {
		return d.(dsnsDoBanco).app
	}
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
//
// Num teste que pediu NovoBanco, é a do banco desse teste. A busca é pelo t
// exato: num subteste, que é outro *testing.T, a chamada cai no banco
// compartilhado. Passe o t de quem chamou NovoBanco.
func AdminDSN(t *testing.T) string {
	t.Helper()
	iniciar(t)
	if d, ok := dsnsPorTeste.Load(t); ok {
		return d.(dsnsDoBanco).admin
	}
	return adminDSN
}

// NovoBanco devolve um pool ligado a um banco só deste teste, migrado e sem
// dados, como o papel da aplicação, e apaga o banco no fim do teste. Ao
// contrário de New, nada é compartilhado com outro teste, e por isso quem o
// usa pode chamar t.Parallel().
//
// O banco é uma cópia do modelo (CREATE DATABASE ... TEMPLATE), tirado logo
// depois das migrations: copiar custa o mesmo que o Restore de New, que
// também apaga e recria um banco a cada teste.
func NovoBanco(t *testing.T) *pgxpool.Pool {
	t.Helper()
	iniciar(t)
	ctx := context.Background()

	// O nome cru vai nas URLs; o sanitizado, entre aspas, no SQL. Os dois
	// saem da mesma leitura do contador, como em EmptyDatabase.
	cru := fmt.Sprintf("stillon_teste_%d", bancos.Add(1))
	nome := pgx.Identifier{cru}.Sanitize()
	comoAdmin(t, func(admin *pgx.Conn) error {
		// #nosec G201 -- nome vem de pgx.Identifier.Sanitize sobre um contador; nada vem de fora.
		_, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s OWNER %s`,
			nome, pgx.Identifier{modelo}.Sanitize(), appUser))
		return err
	}, "criar o banco do teste")

	dsns := dsnsDoBanco{app: trocarBanco(t, appDSN, cru), admin: trocarBanco(t, adminDSN, cru)}
	pool, err := storage.NewPool(ctx, dsns.app)
	if err != nil {
		t.Fatalf("conectar ao banco do teste: %v", err)
	}
	dsnsPorTeste.Store(t, dsns)
	t.Cleanup(func() {
		pool.Close()
		dsnsPorTeste.Delete(t)
		// FORCE derruba as conexões que o próprio teste abriu por fora do
		// pool (DSN, AdminDSN) e esqueceu abertas.
		comoAdmin(t, func(admin *pgx.Conn) error {
			// #nosec G201 -- mesmo identificador sanitizado acima.
			_, err := admin.Exec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, nome))
			return err
		}, "apagar o banco do teste")
	})
	return pool
}

// comoAdmin roda fazer numa conexão de superusuário ao banco de manutenção
// (postgres), aberta e fechada só para isso. Nunca ao stillon_test: o Restore
// de New o apaga, e uma sessão nele travaria o DROP.
func comoAdmin(t *testing.T, fazer func(*pgx.Conn) error, oque string) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, trocarBanco(t, adminDSN, bancoDeManutencao))
	if err != nil {
		t.Fatalf("%s: conectar como admin: %v", oque, err)
	}
	defer admin.Close(ctx)
	if err := fazer(admin); err != nil {
		t.Fatalf("%s: %v", oque, err)
	}
}

// trocarBanco devolve dsn apontando para o banco nome.
func trocarBanco(t *testing.T, dsn, nome string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + nome
	return u.String()
}

// Psql roda script com o psql de dentro do contêiner, como superusuário, no
// banco de teste, e devolve o código de saída e a saída combinada. É para o que
// só o psql faz: metacomandos, variáveis e o código de saída.
func Psql(t *testing.T, script []byte, args ...string) (int, string) {
	t.Helper()
	iniciar(t)
	ctx := context.Background()
	const caminho = "/tmp/stillon-teste.sql"
	if err := container.CopyToContainer(ctx, script, caminho, 0o644); err != nil {
		t.Fatalf("copiar o script para o contêiner: %v", err)
	}
	comando := append([]string{"psql", "--no-psqlrc", "-X", "-q", "-U", adminUser, "-d", dbName}, args...)
	comando = append(comando, "-f", caminho)
	codigo, leitor, err := container.Exec(ctx, comando, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("rodar o psql: %v", err)
	}
	saida, err := io.ReadAll(leitor)
	if err != nil {
		t.Fatalf("ler a saída do psql: %v", err)
	}
	return codigo, string(saida)
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
		testcontainers.WithCmdArgs("-c", "max_connections="+maxConexoes),
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
	if startErr = container.Snapshot(ctx); startErr != nil {
		return
	}
	startErr = criarModelo(ctx)
}

// criarModelo copia o banco recém-migrado (sem sessões abertas depois do
// Snapshot) para o modelo de NovoBanco. É uma cópia própria, e não o modelo
// interno do Snapshot, cujo nome é detalhe do testcontainers.
func criarModelo(ctx context.Context) error {
	u, err := url.Parse(adminDSN)
	if err != nil {
		return err
	}
	// Conectado ao próprio stillon_test, ele não serviria de modelo: o
	// CREATE DATABASE recusa uma origem com sessão aberta.
	u.Path = "/" + bancoDeManutencao
	conn, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return fmt.Errorf("conectar como admin: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+modelo+` TEMPLATE `+dbName+` OWNER `+appUser); err != nil {
		return fmt.Errorf("criar o modelo dos bancos de teste: %w", err)
	}
	return nil
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
