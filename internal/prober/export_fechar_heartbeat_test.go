package prober

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// FecharComReivindicacao roda o fechamento com uma linha de reivindicação dada
// pelo teste. Assim o teste reproduz, sem depender de tempo de parede, a linha
// velha que um tique concorrente pode ler: o ClaimHeartbeatsToClose trava só a
// linha do heartbeat, e o fechamento não a altera, então o READ COMMITTED não
// reavalia o incidente quando a trava é obtida depois do commit do outro tique.
func FecharComReivindicacao(ctx context.Context, pool *pgxpool.Pool, h db.ClaimHeartbeatsToCloseRow, now time.Time) error {
	return storage.InProberTx(ctx, pool, func(q *db.Queries) error {
		return fecharIncidenteDeHeartbeat(ctx, q, h, now)
	})
}
