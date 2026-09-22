// Package storage concentra o acesso ao Postgres: aplicação de migrations,
// construção do pool e os repositórios.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // registra o driver "pgx" no database/sql
	"github.com/pressly/goose/v3"
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
// Usa database/sql em vez do pool pgx porque o goose exige *sql.DB. A conexão
// é aberta e fechada aqui; o pool de trabalho é construído por NewPool.
func Migrate(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("abrir conexão para migrar: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("dialeto do goose: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("aplicar migrations: %w", err)
	}
	return nil
}
