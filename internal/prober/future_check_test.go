package prober

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Sem a guarda do banco, gravar o futuro salva last_checked_at na histerese
// e todas as falhas seguintes são ignoradas até o tempo real alcançá-lo.
func TestFutureCheckCannotPoisonIncidentHysteresis(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://example.com", nil)
	ctx := context.Background()
	var bancoAgora time.Time
	if err := b.pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&bancoAgora); err != nil {
		t.Fatal(err)
	}
	futuro := bancoAgora.Add(24 * time.Hour)
	if err := storage.EnsureChecksPartitions(ctx, b.pool, futuro); err != nil {
		t.Fatal(err)
	}
	claim := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, IntervalSec: 60}
	falha := Result{Status: StatusDown, ErrorKind: KindDNS}
	b.s.gravar(claim, futuro, falha)

	b.comoDono(t, func(tx pgx.Tx) {
		var checks, saude int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM checks WHERE target_id = $1`, alvo).Scan(&checks); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM target_health WHERE target_id = $1`, alvo).Scan(&saude); err != nil {
			t.Fatal(err)
		}
		if checks != 0 || saude != 0 {
			t.Errorf("check futuro deixou %d checks e %d estados de histerese", checks, saude)
		}
	})
	for i := range 3 {
		b.s.gravar(claim, bancoAgora.Add(time.Duration(i-2)*time.Minute), falha)
	}
	incidentes := b.incidentes(t)
	if len(incidentes) != 1 || incidentes[0].ResolvedAt != nil {
		t.Fatalf("três falhas legítimas deveriam abrir um incidente após a recusa, recebeu %+v", incidentes)
	}
}
