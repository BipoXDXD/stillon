package storage_test

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

func valorDe(t *testing.T, reg *prometheus.Registry, nome string) float64 {
	t.Helper()
	familias, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range familias {
		if f.GetName() != nome {
			continue
		}
		m := f.GetMetric()[0]
		if g := m.GetGauge(); g != nil {
			return g.GetValue()
		}
		return m.GetCounter().GetValue()
	}
	t.Fatalf("métrica %s não exposta", nome)
	return 0
}

func TestPoolCollectorReflectsTheLivePool(t *testing.T) {
	pool := testdb.New(t)
	reg := prometheus.NewRegistry()
	reg.MustRegister(storage.NewPoolCollector(pool))

	antes := valorDe(t, reg, "pgxpool_acquire_total")
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := valorDe(t, reg, "pgxpool_acquired_conns"); v != 1 {
		t.Errorf("pgxpool_acquired_conns = %v com uma conexão na mão", v)
	}
	conn.Release()

	if v := valorDe(t, reg, "pgxpool_acquire_total"); v != antes+1 {
		t.Errorf("pgxpool_acquire_total foi de %v para %v, queria +1", antes, v)
	}
	if v := valorDe(t, reg, "pgxpool_max_conns"); v != 10 {
		t.Errorf("pgxpool_max_conns = %v, queria 10 (storage.NewPool)", v)
	}
	for _, nome := range []string{"pgxpool_idle_conns", "pgxpool_total_conns", "pgxpool_empty_acquire_total", "pgxpool_acquire_duration_seconds_total"} {
		valorDe(t, reg, nome)
	}
}
