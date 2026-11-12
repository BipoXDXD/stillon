package prober

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Apagar um alvo PARADO não passa pelo sondador. A série
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

// O log de sinais é um acúmulo: a métrica mostra se o teto e a retenção seguram.
func TestReconcileReportsTheHeartbeatEventLogSize(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	token, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: b.dono, Name: "backup", PingTokenHash: hash, PeriodSec: &periodo, GraceSec: 300,
			Enabled: true, CreatedAt: b.clk.Now(), DeadlineAt: b.clk.Now().Add(time.Hour),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	ping := heartbeat.NewPingService(b.pool, b.clk)
	for _, sig := range []heartbeat.Signal{heartbeat.SignalStart, heartbeat.SignalSuccess, heartbeat.SignalFail} {
		if err := ping.Ping(ctx, token, sig, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(b.m.heartbeatEvents); v != 3 {
		t.Errorf("heartbeat_events = %v, queria 3", v)
	}
}

// Acima do limiar a contagem exata custa uma leitura da tabela inteira a cada 30 s
// (medido: 88 ms a 10^5 linhas), então o gauge passa a usar a estimativa das
// estatísticas do Postgres. Depois de um ANALYZE ela é o valor real; sem ele
// (reltuples = -1, ou desatualizada para baixo) o Reconcile cai na exata.
func TestReconcileReportsTheHeartbeatEventLogSizeAboveTheExactLimit(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	_, hash := heartbeat.NewPingToken()
	periodo := int32(3600)
	err := storage.InUserTx(ctx, b.pool, b.dono, func(q *db.Queries) error {
		return q.InsertHeartbeat(ctx, db.InsertHeartbeatParams{
			ID: id, UserID: b.dono, Name: "backup", PingTokenHash: hash, PeriodSec: &periodo, GraceSec: 300,
			Enabled: true, CreatedAt: b.clk.Now(), DeadlineAt: b.clk.Now().Add(time.Hour),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	total := heartbeatEventsExactBelow + 2000
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.scope', 'prober', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind)
		SELECT uuidv7(), $1, $2, 'success' FROM generate_series(1, $3)`, id, b.clk.Now(), total); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.pool.Exec(ctx, `ANALYZE heartbeat_events`); err != nil {
		t.Fatal(err)
	}
	if err := b.s.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(b.m.heartbeatEvents); v != float64(total) {
		t.Errorf("heartbeat_events = %v, queria %d", v, total)
	}
}

// Uma transição avisa o laço de Run, para que incidents_open não espere a
// reconciliação periódica de 30s.
func TestTransitionSignalsTheReconcileLoop(t *testing.T) {
	b := novaBancada(t, nil, MaxInFlight)
	alvo := b.alvo(t, "https://exemplo.com", nil)
	a := db.ClaimDueTargetsRow{ID: alvo, UserID: b.dono, URL: "https://exemplo.com", Method: "GET"}
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
