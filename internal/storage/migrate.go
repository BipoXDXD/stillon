// Package storage concentra o acesso ao Postgres: aplicação de migrations,
// construção do pool, escopos de RLS e os repositórios.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib" // registra o driver "pgx" no database/sql
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// O //go:embed só enxerga arquivos dentro do diretório do pacote, por isso as
// migrations vivem aqui e não na raiz do repositório (desvio registrado nas
// Global Constraints).
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate aplica todas as migrations pendentes. É chamada no boot, o que
// elimina a classe de bug "esqueci de migrar em produção".
//
// O session locker (advisory lock do Postgres) serializa processos que
// migram ao mesmo tempo. Com instância única ele é inerte; com --role=api e
// --role=prober subindo juntos, sem ele os dois aplicam a mesma migration e
// o segundo cai no boot (item 9b do Plano 1, modo de falha medido: crash, não
// corrupção). O lock é de sessão, e o Postgres o solta sozinho se a conexão
// morrer: um processo derrubado no meio não tranca os outros.
//
// Usa database/sql em vez do pool pgx porque o goose exige *sql.DB.
func Migrate(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("abrir conexão para migrar: %w", err)
	}
	defer db.Close()

	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("abrir migrations embutidas: %w", err)
	}
	// Sonda o lock a cada segundo, por até dois minutos. O padrão do goose
	// (5s) faria cada réplica esperar cinco segundos a mais do que precisa.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 120))
	if err != nil {
		return fmt.Errorf("criar lock de migração: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("preparar migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("aplicar migrations: %w", err)
	}
	return nil
}
