package heartbeat_test

import (
	"context"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// semearComoAdmin grava linhas em massa fora da RLS: o teste precisa de mais
// eventos e incidentes do que o caminho normal produz num tempo razoável.
func semearComoAdmin(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	admin, err := storage.NewPool(ctx, testdb.AdminDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// O limite pedido é o que vale, não o padrão de 50; fora de 1..100, vale o
// padrão. 101 eventos deixam ver as duas pontas da faixa.
func TestEventsHonraOLimitePedidoNaFaixaEVoltaAoPadraoForaDela(t *testing.T) {
	a := novoAmbiente(t)
	id, _ := a.novoHeartbeat(t)
	semearComoAdmin(t, `INSERT INTO heartbeat_events (id, heartbeat_id, at, kind)
		SELECT gen_random_uuid(), $1, $2, 'success' FROM generate_series(1, 101)`, id, agora)
	svc := heartbeat.NewService(a.pool, a.clk)

	for _, c := range []struct {
		limite int32
		quer   int
	}{
		{3, 3}, {1, 1}, {100, 100}, {101, 50}, {0, 50}, {-1, 50},
	} {
		ev, err := svc.Events(context.Background(), a.dono, id, nil, c.limite)
		if err != nil {
			t.Fatalf("Events(limite %d): %v", c.limite, err)
		}
		if len(ev) != c.quer {
			t.Errorf("Events(limite %d) devolveu %d, queria %d", c.limite, len(ev), c.quer)
		}
	}
}

func TestIncidentsHonraOLimitePedido(t *testing.T) {
	a := novoAmbiente(t)
	id, _ := a.novoHeartbeat(t)
	semearComoAdmin(t, `INSERT INTO incidents (id, heartbeat_id, started_at, resolved_at, cause)
		SELECT gen_random_uuid(), $1, $2, $3, 'heartbeat_late' FROM generate_series(1, 3)`,
		id, agora.Add(-time.Hour), agora)
	svc := heartbeat.NewService(a.pool, a.clk)

	for _, c := range []struct {
		limite int32
		quer   int
	}{
		{2, 2}, {1, 1}, {0, 3},
	} {
		inc, err := svc.Incidents(context.Background(), a.dono, id, nil, nil, c.limite)
		if err != nil {
			t.Fatalf("Incidents(limite %d): %v", c.limite, err)
		}
		if len(inc) != c.quer {
			t.Errorf("Incidents(limite %d) devolveu %d, queria %d", c.limite, len(inc), c.quer)
		}
	}
}
