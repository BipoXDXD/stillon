package middleware

import (
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
func RateLimit(perMinute, burst int) func(http.Handler) http.Handler {
	type bucket struct {
		lim  *rate.Limiter
		seen time.Time
	}

	var (
		mu      sync.Mutex
		buckets = map[string]*bucket{}
	)

	go func() {
		for range time.Tick(time.Minute) {
			mu.Lock()
			for k, b := range buckets {
				if time.Since(b.seen) > 10*time.Minute {
					delete(buckets, k)
				}
			}
			mu.Unlock()
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
