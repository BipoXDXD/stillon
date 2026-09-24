// Package observability reúne o registry de métricas Prometheus da
// aplicação e o servidor HTTP, em loopback, que o expõe.
package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// NewRegistry devolve o registry da aplicação, com as métricas de runtime do
// Go e do processo. É um registry próprio, e não o global, para que só
// entre o que o código registra de propósito: uma dependência que se
// registrasse sozinha no global não apareceria em /metrics sem ninguém ver.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// NewMetricsServer serve GET /metrics em addr, e nada mais. A config garante
// que addr é loopback (spec §5.7, D10); este servidor não tem autenticação
// porque não precisa, já que não é alcançável de fora.
func NewMetricsServer(addr string, reg *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
