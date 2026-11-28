package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A versão dos termos e o instante do aceite andam juntos: o CHECK recusa uma
// sem a outra, e aceita as duas NULL (o INSERT do binário anterior) ou as duas
// preenchidas.
func TestAceiteDosTermosExigeParDeColunas(t *testing.T) {
	testdb.New(t)
	admin := testdb.Admin(t)
	ctx := context.Background()
	casos := []struct {
		nome, sql string
		recusa    bool
	}{
		{"só a versão", `INSERT INTO users (id, email, password_hash, terms_version) VALUES (gen_random_uuid(), 'a@exemplo.com', 'x', '2026-11-28')`, true},
		{"só o instante", `INSERT INTO users (id, email, password_hash, terms_accepted_at) VALUES (gen_random_uuid(), 'b@exemplo.com', 'x', now())`, true},
		{"nenhuma", `INSERT INTO users (id, email, password_hash) VALUES (gen_random_uuid(), 'c@exemplo.com', 'x')`, false},
		{"as duas", `INSERT INTO users (id, email, password_hash, terms_version, terms_accepted_at) VALUES (gen_random_uuid(), 'd@exemplo.com', 'x', '2026-11-28', now())`, false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := admin.Exec(ctx, c.sql)
			var pgErr *pgconn.PgError
			recusado := errors.As(err, &pgErr) && pgErr.Code == "23514" && pgErr.ConstraintName == "users_aceite_em_par"
			if c.recusa && !recusado {
				t.Fatalf("queria 23514 users_aceite_em_par, recebeu %v", err)
			}
			if !c.recusa && err != nil {
				t.Fatalf("INSERT recusado: %v", err)
			}
		})
	}
}
