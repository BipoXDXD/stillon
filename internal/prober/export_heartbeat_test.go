package prober

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// NewHeartbeatEvaluatorComPagina deixa o teste exercitar a paginação sem criar
// cem heartbeats.
func NewHeartbeatEvaluatorComPagina(pool *pgxpool.Pool, clk clock.Clock, pagina int32) *HeartbeatEvaluator {
	e := NewHeartbeatEvaluator(pool, clk)
	e.pageSize = pagina
	return e
}
