// Package httpapi expõe a API HTTP do StillOn.
package httpapi

import (
	"net/http"
	"time"

	"github.com/BipoXDXD/stillon/internal/config"
)

// NewServer configura o http.Server com todos os timeouts.
//
// ReadHeaderTimeout é o mais importante: sem ele o servidor aceita Slowloris,
// em que o cliente envia headers um byte por minuto e ocupa uma conexão
// indefinidamente até esgotar o servidor (spec §5.7).
func NewServer(cfg *config.Config, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}
