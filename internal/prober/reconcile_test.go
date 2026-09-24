package prober

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// D9/M1 do Plano 2: apagar um alvo PARADO não passa pelo sondador. A série
// dele só some na reconciliação.
func TestReconcileForgetsSeriesOfDeletedTargets(t *testing.T) {
	srv, client := servidorLiberado(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	b := novaBancada(t, client, MaxInFlight)
	vivo := b.alvo(t, srv.URL, nil)
	apagado := b.alvo(t, srv.URL, nil)
	b.rodada(t)
	if n := testutil.CollectAndCount(b.m.duration); n != 2 {
		t.Fatalf("%d séries depois de sondar dois alvos", n)
	}

	ctx := context.Background()
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		_, err := q.DeleteTarget(ctx, db.DeleteTargetParams{UserID: b.dono, ID: apagado})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	if n := testutil.CollectAndCount(b.m.duration); n != 1 {
		t.Errorf("%d séries depois da reconciliação, queria 1", n)
	}
	if vistos := b.m.alvosVistos(); len(vistos) != 1 || vistos[0] != vivo {
		t.Errorf("alvos vistos = %v, queria só o vivo %s", vistos, vivo)
	}
}

func TestReconcileReportsOpenIncidents(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	for range 2 {
		alvo := b.alvo(t, "https://exemplo.com", nil)
		err := storage.InProberTx(ctx, b.pool, func(q *db.Queries) error {
			return q.OpenIncident(ctx, db.OpenIncidentParams{
				ID: uuid.Must(uuid.NewV7()), TargetID: alvo, StartedAt: b.clk.Now(), Cause: "dns",
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(b.m.incidentsOpen); v != 2 {
		t.Errorf("incidents_open = %v, queria 2", v)
	}
}

// Uma transição avisa o laço de Run, para que incidents_open não espere a
// reconciliação periódica de 30s.
func TestTransitionSignalsTheReconcileLoop(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono}
	falha := Result{Status: StatusDown, ErrorKind: KindDNS}

	b.s.gravar(a, b.clk.Now(), falha)
	b.clk.Advance(time.Minute)
	b.s.gravar(a, b.clk.Now(), falha)
	if len(b.s.transicao) != 0 {
		t.Fatal("duas falhas, sem transição, já avisaram o laço")
	}
	b.clk.Advance(time.Minute)
	b.s.gravar(a, b.clk.Now(), falha)
	if len(b.s.transicao) != 1 {
		t.Error("a abertura do incidente não avisou o laço de reconciliação")
	}
}
