package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// EnsureChecksPartitions garante as partições de checks do mês corrente e do
// seguinte. É idempotente.
//
// O mês é calculado em UTC, como as bordas que a função SQL grava. Às 23:30
// do dia 30 em São Paulo já é dia 1 em UTC, e a sonda gravada naquele
// instante pertence à partição do mês NOVO. Calcular no fuso local criaria
// a partição errada, e o INSERT falharia com "no partition of relation".
func EnsureChecksPartitions(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	u := now.UTC()
	atual := time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	q := db.New(pool)
	for _, mes := range []time.Time{atual, atual.AddDate(0, 1, 0)} {
		if err := q.EnsureChecksPartition(ctx, mes); err != nil {
			return fmt.Errorf("criar partição de checks %s: %w", mes.Format("2006-01"), err)
		}
	}
	return nil
}

// MaintainChecksPartitions repete EnsureChecksPartitions até ctx acabar. A
// partição do mês seguinte existe desde o boot, então um tick diário tem um
// mês inteiro de folga. O Plano 4 acrescenta aqui o DROP das partições
// vencidas (spec §12).
func MaintainChecksPartitions(ctx context.Context, pool *pgxpool.Pool, clk clock.Clock, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := EnsureChecksPartitions(ctx, pool, clk.Now()); err != nil && ctx.Err() == nil {
				slog.Error("manter partições de checks", "erro", err)
			}
		}
	}
}
