package storage

import (
	"context"
	"database/sql"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// MigrateUpTo aplica as migrations só até version, para o teste montar o
// estado de um banco que parou numa versão antiga.
func MigrateUpTo(ctx context.Context, dsn string, version int64) error {
	provider, closeDB, err := testProvider(dsn)
	if err != nil {
		return err
	}
	defer closeDB()
	_, err = provider.UpTo(ctx, version)
	return err
}

// MigrateDownTo desfaz as migrations até version, como faria quem reverte
// um deploy com o goose. Só os testes descem: o boot só sobe.
func MigrateDownTo(ctx context.Context, dsn string, version int64) error {
	provider, closeDB, err := testProvider(dsn)
	if err != nil {
		return err
	}
	defer closeDB()
	_, err = provider.DownTo(ctx, version)
	return err
}

func testProvider(dsn string) (*goose.Provider, func(), error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, err
	}
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return provider, func() { db.Close() }, nil
}

// RollupLockKey deixa o teste segurar a mesma trava da consolidação.
const RollupLockKey = rollupLockKey
