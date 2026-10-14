package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// O banco amarra as latências do rollup como a consolidação as calcula: as
// quatro ficam NULL juntas (hora sem sucesso) ou presentes juntas, nunca
// negativas, e em ordem p50 <= p95 <= p99 <= max (percentis da mesma amostra).
// Uma escrita fora da consulta gerada não consegue gravar um rollup que o
// painel mostraria como uma curva impossível.
func TestRollupLatenciesMustBeCoherent(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dono := novoUsuario(t, pool)
	alvo := novoAlvo(t, pool, dono, agora)

	ptr := func(n int32) *int32 { return &n }
	for i, tc := range []struct {
		nome                string
		ok                  int32
		p50, p95, p99, maxi *int32
		recusa              bool
	}{
		{"latencias_em_ordem", 2, ptr(10), ptr(20), ptr(30), ptr(40), false},
		{"latencias_iguais", 1, ptr(0), ptr(0), ptr(0), ptr(0), false},
		{"hora_sem_sucesso", 0, nil, nil, nil, nil, false},
		{"p95_abaixo_do_p50", 2, ptr(20), ptr(10), ptr(30), ptr(40), true},
		{"p99_abaixo_do_p95", 2, ptr(10), ptr(30), ptr(20), ptr(40), true},
		{"max_abaixo_do_p99", 2, ptr(10), ptr(20), ptr(40), ptr(30), true},
		{"p50_negativo", 2, ptr(-1), ptr(0), ptr(0), ptr(0), true},
		{"p95_ausente_com_sucesso", 2, ptr(10), nil, ptr(30), ptr(40), true},
		{"p99_ausente_com_sucesso", 2, ptr(10), ptr(20), nil, ptr(40), true},
		{"max_ausente_com_sucesso", 2, ptr(10), ptr(20), ptr(30), nil, true},
		{"max_presente_sem_sucesso", 0, nil, nil, nil, ptr(40), true},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `SELECT set_config('app.user_id', $1, true)`, dono.String()); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO check_rollup_1h (target_id, bucket, total, ok, p50, p95, p99, max)
				VALUES ($1, $2, 2, $3, $4, $5, $6, $7)`,
				alvo, agora.Add(time.Duration(i)*time.Hour), tc.ok, tc.p50, tc.p95, tc.p99, tc.maxi)

			var pgErr *pgconn.PgError
			switch {
			case tc.recusa && !(errors.As(err, &pgErr) && pgErr.Code == "23514"):
				t.Errorf("rollup incoerente: erro = %v, queria check_violation (23514)", err)
			case !tc.recusa && err != nil:
				t.Errorf("rollup coerente recusado: %v", err)
			}
		})
	}
}
