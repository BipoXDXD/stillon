// Package testdb sobe um Postgres real para os testes de integração.
//
// A spec (§14) proíbe mock de banco: RLS, SKIP LOCKED e violação de
// constraint são invisíveis contra um mock, e são justamente o que precisa de
// teste. O contêiner sobe uma vez por execução de pacote; cada teste recebe o
// banco restaurado ao estado pós-migration via snapshot.
package testdb

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/BipoXDXD/vigilume/internal/storage"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	dsn       string
	startErr  error
)

const (
	dbName = "vigilume_test"
	dbUser = "vigilume"
	dbPass = "vigilume"
)

// New devolve um pool ligado a um Postgres limpo e migrado.
//
// Pula com -short: quem não tem Docker disponível ainda consegue rodar os
// testes dos pacotes puros (crypto, safedial, password) com `make test-short`.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("precisa de Docker; pulado em -short")
	}

	once.Do(start)
	if startErr != nil {
		t.Fatalf("subir Postgres de teste: %v", startErr)
	}

	ctx := context.Background()
	pool, err := storage.NewPool(ctx, dsn)
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

func start() {
	ctx := context.Background()

	container, startErr = tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername(dbUser),
		tcpostgres.WithPassword(dbPass),
		tcpostgres.WithSQLDriver("pgx"), // exigido por Snapshot/Restore
		tcpostgres.BasicWaitStrategies(),
	)
	if startErr != nil {
		return
	}

	dsn, startErr = container.ConnectionString(ctx, "sslmode=disable")
	if startErr != nil {
		return
	}

	// O snapshot é tirado com o schema já migrado e sem dados, então cada
	// teste começa exatamente daqui.
	if startErr = storage.Migrate(ctx, dsn); startErr != nil {
		return
	}
	startErr = container.Snapshot(ctx)
}

// Terminate derruba o contêiner. Chame de um TestMain quando quiser
// encerramento explícito; o Ryuk do testcontainers já limpa sozinho depois.
func Terminate() {
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
}
