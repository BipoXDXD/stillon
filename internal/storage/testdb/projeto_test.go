package testdb

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// guardaTx abre a transação pelo pool e guarda o pgx.Tx: db.Queries não expõe
// SQL avulso, e o current_setting precisa rodar na conexão da transação.
type guardaTx struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

func (g *guardaTx) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := g.pool.Begin(ctx)
	g.tx = tx
	return tx, err
}

// As sementes dos testes rodam no escopo de projeto: depois da troca das
// políticas por pertença ao projeto, o escopo de usuário não vê nem grava
// recurso nenhum. Sabotar noProjetoPadrao para rodar fn em storage.InUserTx
// deixa app.project_id vazio e este teste vermelho.
func TestNoProjetoPadraoUsesProjectScope(t *testing.T) {
	pool := New(t)
	ctx := context.Background()
	dono := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`,
		dono, dono.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}

	g := &guardaTx{pool: pool}
	var visto uuid.UUID
	var escopo string
	err := noProjetoPadrao(t, g, dono, func(_ *db.Queries, projeto uuid.UUID) error {
		visto = projeto
		return g.tx.QueryRow(ctx, `SELECT coalesce(current_setting('app.project_id', true), '')`).Scan(&escopo)
	})
	if err != nil {
		t.Fatal(err)
	}
	padrao := ProjetoPadrao(t, dono)
	if visto != padrao {
		t.Errorf("projeto entregue a fn = %s, queria o Projeto padrão %s", visto, padrao)
	}
	if escopo != padrao.String() {
		t.Errorf("app.project_id dentro de fn = %q, queria %q", escopo, padrao)
	}

	// O exportado é o mesmo caminho: fn roda e recebe o Projeto padrão.
	err = NoProjetoPadrao(t, pool, dono, func(_ *db.Queries, projeto uuid.UUID) error {
		visto = projeto
		return nil
	})
	if err != nil || visto != padrao {
		t.Errorf("NoProjetoPadrao: projeto = %s, err = %v; queria %s", visto, err, padrao)
	}
}
