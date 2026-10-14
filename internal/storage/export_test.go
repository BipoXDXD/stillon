package storage

import (
	"context"
	"database/sql"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// MigrateDownTo desfaz as migrations até version, como faria quem reverte
// um deploy com o goose. Só os testes descem: o boot só sobe.
func MigrateDownTo(ctx context.Context, dsn string, version int64) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return err
	}
	_, err = provider.DownTo(ctx, version)
	return err
}
