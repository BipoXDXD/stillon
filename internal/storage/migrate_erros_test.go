package storage_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// Sem Docker: o DSN é recusado antes de qualquer conexão. O sql.Open do pgx é
// preguiçoso, então a recusa chega pelo Up; o erro vai para o log do boot e
// não pode carregar a senha que estava no DSN.
func TestMigrateRejectsMalformedDSN(t *testing.T) {
	const senha = "senha-canario-do-dsn"
	err := storage.Migrate(context.Background(), "postgres://app:"+senha+"@localhost:porta/stillon")
	if err == nil {
		t.Fatal("DSN com porta não numérica deu nil, queria erro")
	}
	if strings.Contains(err.Error(), senha) {
		t.Errorf("erro = %q, vazou a senha do DSN", err)
	}
}

// Com o contexto já cancelado, o boot falha e o banco fica como estava: nem a
// tabela de versões do goose nem a da primeira migration aparecem.
func TestMigrateWithCanceledContextAppliesNothing(t *testing.T) {
	dsn := testdb.EmptyDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := storage.Migrate(ctx, dsn); err == nil {
		t.Fatal("Migrate com contexto cancelado deu nil, queria erro")
	}

	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	for _, tabela := range []string{"goose_db_version", "users"} {
		var existe bool
		if err := conn.QueryRow(context.Background(),
			`SELECT to_regclass('public.' || $1) IS NOT NULL`, tabela).Scan(&existe); err != nil {
			t.Fatal(err)
		}
		if existe {
			t.Errorf("tabela %s existe depois do Migrate cancelado, queria banco intocado", tabela)
		}
	}
}
