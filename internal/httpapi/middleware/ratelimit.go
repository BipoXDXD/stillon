package middleware

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/BipoXDXD/sentinela/internal/apierr"
)

// RateLimit aplica token bucket por cliente (spec §5.7).
//
// Os buckets ficam em memória e são varridos periodicamente. Um mapa que só
// cresce seria vazamento de memória e vetor de exaustão: basta o atacante
// variar o IP de origem.
//
// A varredura recebe ctx para poder parar. time.Tick nunca é coletado pelo
// GC — não há como cancelá-lo — então a goroutine de limpeza precisaria de
// um jeito explícito de terminar; amarrar isso ao contexto do servidor faz
// a goroutine morrer no shutdown gracioso junto com o resto, em vez de
// vazar para sempre (R32).
func RateLimit(ctx context.Context, perMinute, burst int) func(http.Handler) http.Handler {
	type bucket struct {
		lim  *rate.Limiter
		seen time.Time
	}

	var (
		mu      sync.Mutex
		buckets = map[string]*bucket{}
	)

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				for k, b := range buckets {
					if time.Since(b.seen) > 10*time.Minute {
						delete(buckets, k)
					}
				}
				mu.Unlock()
			}
		}
	}()

	limit := rate.Limit(float64(perMinute) / 60.0)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}

			mu.Lock()
			b, ok := buckets[ip]
			if !ok {
				b = &bucket{lim: rate.NewLimiter(limit, burst)}
				buckets[ip] = b
			}
			b.seen = time.Now()
			allow := b.lim.Allow()
			mu.Unlock()

			if !allow {
				apierr.WriteError(w, r, apierr.CodeRateLimited)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
