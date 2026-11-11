package storage_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// applicationNameDe lê o application_name da sessão, como o pg_stat_activity o
// mostraria ao operador.
func applicationNameDe(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var nome string
	if err := pool.QueryRow(context.Background(), `SELECT current_setting('application_name')`).Scan(&nome); err != nil {
		t.Fatal(err)
	}
	return nome
}

// dsnComApplicationName devolve o DSN de teste com application_name na query.
func dsnComApplicationName(t *testing.T, nome string) string {
	t.Helper()
	u, err := url.Parse(testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("application_name", nome)
	u.RawQuery = q.Encode()
	return u.String()
}

// Sem application_name, o pg_stat_activity não diz quais conexões são do
// StillOn nem de qual processo. O pool marca todas as suas conexões.
func TestPoolSetsApplicationNameByDefault(t *testing.T) {
	pool := testdb.New(t)
	if got := applicationNameDe(t, pool); got != "stillon" {
		t.Errorf("application_name = %q, queria %q", got, "stillon")
	}
}

func TestNewPoolNamedUsesTheGivenName(t *testing.T) {
	testdb.New(t) // sobe o banco e restaura o snapshot ao fim
	pool, err := storage.NewPoolNamed(context.Background(), testdb.DSN(t), "stillon-prober")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if got := applicationNameDe(t, pool); got != "stillon-prober" {
		t.Errorf("application_name = %q, queria %q", got, "stillon-prober")
	}
}

// O que o operador pôs no DSN vence o nome do processo.
func TestApplicationNameFromTheDSNWins(t *testing.T) {
	testdb.New(t)
	pool, err := storage.NewPoolNamed(context.Background(), dsnComApplicationName(t, "do-operador"), "stillon-api")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if got := applicationNameDe(t, pool); got != "do-operador" {
		t.Errorf("application_name = %q, queria %q (o do DSN)", got, "do-operador")
	}
}
