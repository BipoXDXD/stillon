package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A guarda deve impedir um relógio adiantado de contaminar a histerese,
// inclusive quando o INSERT vem de fora da query gerada. Remover o trigger
// faz os casos de rejeição ficarem vermelhos; retirar só a margem quebra o
// caso de pequena diferença entre os relógios.
func TestChecksRejectDatesAheadOfDatabaseClock(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	var bancoAgora time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&bancoAgora); err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureChecksPartitions(ctx, pool, bancoAgora); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		nome   string
		quando time.Time
		recusa bool
	}{
		{"passado", bancoAgora.Add(-time.Minute), false},
		{"pequeno_desvio", bancoAgora.Add(30 * time.Second), false},
		{"relogio_adiantado", bancoAgora.Add(24 * time.Hour), true},
		{"infinito", time.Time{}, true},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			var err error
			if tc.nome == "infinito" {
				// SQL direto é outra porta de entrada e não usa time.Time.
				// A partição default temporária recebe infinity para que
				// a falha venha da guarda, e não da ausência de partição.
				if _, err := pool.Exec(ctx, `CREATE TABLE checks_restante PARTITION OF checks DEFAULT`); err != nil {
					t.Fatal(err)
				}
				tx, abrirErr := pool.Begin(ctx)
				if abrirErr != nil {
					t.Fatal(abrirErr)
				}
				defer tx.Rollback(ctx)
				if _, err = tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
					t.Fatal(err)
				}
				_, err = tx.Exec(ctx, `INSERT INTO checks (id, target_id, checked_at, status, latency_ms)
					VALUES ($1, $2, 'infinity', 'up', 12)`, novoID(), alvo)
			} else {
				err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
					return q.InsertCheck(ctx, db.InsertCheckParams{
						ID: novoID(), TargetID: alvo, CheckedAt: tc.quando,
						Status: "up", LatencyMS: 12,
					})
				})
			}
			if !tc.recusa {
				if err != nil {
					t.Fatalf("check legítimo recusado: %v", err)
				}
				return
			}
			exigirCheckFuturoRecusado(t, err)
		})
	}
}

func TestChecksRejectFutureUpdatesAndNewPartitions(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)
	var bancoAgora time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&bancoAgora); err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureChecksPartitions(ctx, pool, bancoAgora); err != nil {
		t.Fatal(err)
	}
	novoCheck(t, pool, alvo, bancoAgora.Add(-time.Minute))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE checks SET checked_at = $1 WHERE target_id = $2`, bancoAgora.Add(24*time.Hour), alvo)
	_ = tx.Rollback(ctx)
	exigirCheckFuturoRecusado(t, err)

	// Uma partição criada DEPOIS da migration também precisa da proteção.
	novoMes := bancoAgora.AddDate(0, 2, 0)
	if err := storage.EnsureChecksPartitions(ctx, pool, novoMes); err != nil {
		t.Fatal(err)
	}
	err = storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return q.InsertCheck(ctx, db.InsertCheckParams{
			ID: novoID(), TargetID: alvo, CheckedAt: novoMes,
			Status: "up", LatencyMS: 12,
		})
	})
	exigirCheckFuturoRecusado(t, err)
	if n := contarComEscopo(t, pool, dono, "checks"); n != 1 {
		t.Fatalf("%d checks depois das recusas, queria apenas o legítimo", n)
	}
}

func exigirCheckFuturoRecusado(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "checks_checked_at_not_future" {
		t.Fatalf("check futuro deveria violar checks_checked_at_not_future (23514), recebeu %v", err)
	}
}
