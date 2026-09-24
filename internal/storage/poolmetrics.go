package storage

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// poolCollector lê pool.Stat() a cada coleta, em vez de manter contadores
// próprios: o pgxpool já os mantém, e duas contagens divergiriam.
type poolCollector struct {
	pool                                  *pgxpool.Pool
	acquired, idle, total, max            *prometheus.Desc
	acquireCount, emptyAcquire, acquireDu *prometheus.Desc
}

// NewPoolCollector expõe o estado do pool (spec §13). O sinal que importa é
// pgxpool_empty_acquire_total subindo: sondas esperando conexão, com o
// semáforo de 64 contra um pool de 10.
func NewPoolCollector(pool *pgxpool.Pool) prometheus.Collector {
	d := func(nome, ajuda string) *prometheus.Desc {
		return prometheus.NewDesc("pgxpool_"+nome, ajuda, nil, nil)
	}
	return &poolCollector{
		pool:         pool,
		acquired:     d("acquired_conns", "Conexões em uso agora."),
		idle:         d("idle_conns", "Conexões ociosas agora."),
		total:        d("total_conns", "Conexões abertas agora."),
		max:          d("max_conns", "Teto de conexões do pool."),
		acquireCount: d("acquire_total", "Aquisições de conexão desde o boot."),
		emptyAcquire: d("empty_acquire_total", "Aquisições que encontraram o pool sem conexão ociosa."),
		acquireDu:    d("acquire_duration_seconds_total", "Tempo total gasto adquirindo conexões."),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.max, c.acquireCount, c.emptyAcquire, c.acquireDu} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(c.acquired, float64(s.AcquiredConns()))
	gauge(c.idle, float64(s.IdleConns()))
	gauge(c.total, float64(s.TotalConns()))
	gauge(c.max, float64(s.MaxConns()))
	counter(c.acquireCount, float64(s.AcquireCount()))
	counter(c.emptyAcquire, float64(s.EmptyAcquireCount()))
	counter(c.acquireDu, s.AcquireDuration().Seconds())
}
