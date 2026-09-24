package prober

import (
	"sync"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics são as métricas do sondador (spec §13).
type Metrics struct {
	duration      *prometheus.HistogramVec
	total         *prometheus.CounterVec
	inflight      prometheus.Gauge
	queueDepth    prometheus.Gauge
	incidentsOpen prometheus.Gauge

	// vistos é o conjunto de alvos com série em duration. A reconciliação o
	// confronta com o banco para apagar as séries de alvos que sumiram.
	mu     sync.Mutex
	vistos map[uuid.UUID]struct{}
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "probe_duration_seconds",
			Help: "Latência das sondas, por alvo.",
			// Até 30s, o timeout máximo de um alvo.
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
		}, []string{"target_id"}),
		total: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "probe_total",
			Help: "Sondas concluídas, por estado.",
		}, []string{"status"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scheduler_inflight",
			Help: "Sondas em voo agora.",
		}),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "scheduler_queue_depth",
			Help: "Alvos vencidos que não couberam no último despacho.",
		}),
		// É o total do banco, não o deste processo: com vários sondadores,
		// todos reportam o mesmo número. Agregue com max, nunca com sum.
		incidentsOpen: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "incidents_open",
			Help: "Incidentes abertos agora, em todos os alvos.",
		}),
		vistos: map[uuid.UUID]struct{}{},
	}
	reg.MustRegister(m.duration, m.total, m.inflight, m.queueDepth, m.incidentsOpen)
	return m
}

func (m *Metrics) observe(alvo uuid.UUID, r Result) {
	m.duration.WithLabelValues(alvo.String()).Observe(r.Latency.Seconds())
	m.total.WithLabelValues(string(r.Status)).Inc()
	m.mu.Lock()
	m.vistos[alvo] = struct{}{}
	m.mu.Unlock()
}

// forget apaga a série de um alvo que deixou de existir (D9 do Plano 2).
// Sem isso, cada alvo apagado deixaria uma série viva até o restart.
func (m *Metrics) forget(alvo uuid.UUID) {
	m.duration.DeleteLabelValues(alvo.String())
	m.mu.Lock()
	delete(m.vistos, alvo)
	m.mu.Unlock()
}

func (m *Metrics) alvosVistos() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]uuid.UUID, 0, len(m.vistos))
	for id := range m.vistos {
		ids = append(ids, id)
	}
	return ids
}
