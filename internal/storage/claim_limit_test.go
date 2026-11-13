package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// transacaoComReexecucao abre uma transação do sondador em que o planejador só
// tem um caminho barato: Nested Loop Semi Join com a subconsulta do LIMIT no
// lado de dentro, sem Materialize. Ela roda de novo a cada linha de fora, e as
// linhas que o próprio comando já alterou são puladas pelo LockRows. Com os
// parâmetros padrão o planejador põe a subconsulta do lado de fora; estes os
// tiram do caminho para exercitar o plano que o SQL não pode permitir.
func transacaoComReexecucao(t *testing.T, pool *pgxpool.Pool) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	for _, parametro := range []string{
		"enable_hashjoin", "enable_mergejoin", "enable_hashagg", "enable_sort", "enable_material", "enable_memoize",
	} {
		if _, err := tx.Exec(ctx, `SELECT set_config($1, 'off', true)`, parametro); err != nil {
			t.Fatal(err)
		}
	}
	return tx
}

// O teto do lote é o que dimensiona as vagas do sondador e o lease do
// dispatcher: tem de valer em qualquer plano.
func TestClaimDueTargetsRespectsTheLimitInAnyPlan(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	for range 3 {
		novoAlvo(t, pool, dono, agora.Add(-time.Minute))
	}

	tx := transacaoComReexecucao(t, pool)
	alvos, err := db.New(tx).ClaimDueTargets(ctx, db.ClaimDueTargetsParams{Now: agora, MaxRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(alvos) != 1 {
		t.Errorf("reivindicou %d alvos com MaxRows 1", len(alvos))
	}
}

func TestClaimDueNotificationsRespectsTheLimitInAnyPlan(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	incidente := novoIncidente(t, pool, novoAlvo(t, pool, dono, agora))
	for range 3 {
		novaNotificacao(t, pool, incidente, novoCanal(t, pool, dono, nil, time.Time{}))
	}

	tx := transacaoComReexecucao(t, pool)
	lote, err := db.New(tx).ClaimDueNotifications(ctx, db.ClaimDueNotificationsParams{
		Now: agora, LeaseUntil: agora.Add(time.Minute), MaxRows: 1,
		Kinds: kindsConhecidos, Events: eventosConhecidos,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lote) != 1 {
		t.Errorf("reivindicou %d notificações com MaxRows 1", len(lote))
	}
}
