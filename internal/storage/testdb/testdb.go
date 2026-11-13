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
	// produção.
	appUser = "stillon_app"
	appPass = "stillon_app"

	// snapshotName é o banco-molde de que restaurar recria o banco de teste.
	snapshotName = "stillon_migrado"
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
		if err := restaurar(ctx); err != nil {
			t.Fatalf("restaurar snapshot: %v", err)
		}
	})
	return pool
}

// restaurar devolve o banco de teste ao estado do snapshot.
//
// Substitui o container.Restore do testcontainers-go, que vaza uma conexão de
// superusuário por chamada: o snapshotConnection do módulo postgres devolve um
// *sql.Conn que ninguém fecha (o comentário dele diz que o pool.Close o fecharia,
// mas sql.DB.Close não fecha conexão em uso). A conexão só some quando o GC
// finaliza o socket, e o pacote targets chegava ao max_connections (53300 para
// o papel da aplicação). Código upstream, igual ainda na v0.45.0:
// https://github.com/testcontainers/testcontainers-go/blob/f31e98d4ee9b/modules/postgres/postgres.go#L352-L380
//
// Este restore pode sair quando o upstream fechar a conexão. Antes de trocar,
// aponte o TestRestaurarNaoVazaConexaoDeAdmin para o container.Restore e veja-o
// passar.
//
// Precisa de zero conexões abertas no snapshot; as do banco alvo são derrubadas
// aqui, mas o chamador fecha o pool antes.
func restaurar(ctx context.Context) error {
	admin, err := pgx.Connect(ctx, adminDSNDoBancoPostgres())
	if err != nil {
		return fmt.Errorf("conectar como admin ao banco postgres: %w", err)
	}
	defer admin.Close(ctx)

	alvo := pgx.Identifier{dbName}.Sanitize()
	molde := pgx.Identifier{snapshotName}.Sanitize()
	dono := pgx.Identifier{adminUser}.Sanitize()

	// Terminar antes do DROP ... WITH (FORCE) evita a falha ocasional dele e
	// libera o snapshot, que não pode ter conexão para servir de template.
	if _, err := admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = ANY($1) AND pid <> pg_backend_pid()`, []string{dbName, snapshotName}); err != nil {
		return fmt.Errorf("terminar conexões antes de restaurar: %w", err)
	}
	// #nosec G201 -- identificadores constantes, sanitizados por pgx.Identifier.
	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, alvo)); err != nil {
		return fmt.Errorf("apagar banco de teste: %w", err)
	}
	// #nosec G201 -- idem.
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s WITH TEMPLATE %s OWNER %s`, alvo, molde, dono)); err != nil {
		return fmt.Errorf("recriar banco de teste do snapshot: %w", err)
	}
	return nil
}

// adminDSNDoBancoPostgres aponta o admin para o banco "postgres": o banco de
// teste é apagado e recriado, então não dá para ficar conectado nele.
func adminDSNDoBancoPostgres() string {
	u, err := url.Parse(adminDSN)
	if err != nil {
		// adminDSN vem de ConnectionString e já foi lido em start.
		panic(fmt.Errorf("DSN de admin inválido: %w", err))
	}
	u.Path = "/postgres"
	return u.String()
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

// Admin é um pool de superusuário no banco de teste, para o teste preparar e
// reler as tabelas com RLS "por fora" da política, como faria quem tivesse um
// dump: o papel da aplicação, sem escopo, não vê nada nelas. Chame depois de
// New: o pool fecha no cleanup, antes de o snapshot voltar. É um pool por
// teste, aberto na primeira chamada e reaproveitado nas seguintes, e a conexão
// só abre no primeiro uso.
func Admin(t *testing.T) *pgxpool.Pool {
	t.Helper()
	iniciar(t)
	if pool, ok := poolsDeAdmin.Load(t); ok {
		return pool.(*pgxpool.Pool)
	}
	pool, err := pgxpool.New(context.Background(), adminDSN)
	if err != nil {
		t.Fatalf("abrir pool de admin: %v", err)
	}
	if outro, carregado := poolsDeAdmin.LoadOrStore(t, pool); carregado {
		pool.Close() // outra goroutine do mesmo teste chegou antes
		return outro.(*pgxpool.Pool)
	}
	t.Cleanup(func() {
		poolsDeAdmin.Delete(t)
		pool.Close()
	})
	return pool
}

// poolsDeAdmin guarda o pool de Admin de cada teste em curso.
var poolsDeAdmin sync.Map

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
// por digest; a CI escolhe a versão por TEST_POSTGRES_IMAGE.
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
	startErr = container.Snapshot(ctx, tcpostgres.WithSnapshotName(snapshotName))
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
