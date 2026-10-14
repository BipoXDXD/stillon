package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// requisicaoDeOrigem monta uma requisição de um IPv4 distinto por n: cada n
// é uma origem inédita para o limitador.
func requisicaoDeOrigem(n uint32) *http.Request {
	ip := netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.RemoteAddr = netip.AddrPortFrom(ip, 1234).String()
	return r
}

// BenchmarkRateLimitOrigemNovaComMapaCheio mede o caso do ataque do E6: o
// mapa está no teto real (maxBuckets), todas as entradas são recentes, e
// cada requisição vem de uma origem inédita, que obriga a despejar uma.
func BenchmarkRateLimitOrigemNovaComMapaCheio(b *testing.B) {
	h := rateLimit(b.Context(), clock.Real{}, porMinuto(60), 5, porIP(Proxies{}), maxBuckets)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	var n uint32 = 1 << 24
	for range maxBuckets {
		h.ServeHTTP(httptest.NewRecorder(), requisicaoDeOrigem(n))
		n++
	}
	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), requisicaoDeOrigem(n))
		n++
	}
}
