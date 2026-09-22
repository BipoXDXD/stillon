package middleware

import (
	"context"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/BipoXDXD/sentinela/internal/apierr"
)

// maxBuckets limita quantas origens distintas o limitador acompanha ao mesmo
// tempo. Sem teto, o mapa é um vetor de exaustão de memória: cada origem
// inédita cria uma entrada que vive no mínimo 10 minutos, e um /64 de IPv6
// oferece endereços de graça. Ver o comentário de chaveDe sobre por que o
// agrupamento por /64 é a metade barata dessa defesa.
const maxBuckets = 100_000

// RateLimit aplica token bucket por cliente (spec §5.7).
//
// A varredura recebe ctx para poder parar. time.Tick nunca é coletado pelo
// GC — não há como cancelá-lo — então a goroutine de limpeza precisaria de
// um jeito explícito de terminar; amarrar isso ao contexto do servidor faz
// a goroutine morrer no shutdown gracioso junto com o resto, em vez de
// vazar para sempre (R32).
func RateLimit(ctx context.Context, perMinute, burst int, trusted []netip.Prefix) func(http.Handler) http.Handler {
	return rateLimit(ctx, perMinute, burst, trusted, maxBuckets)
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

type limiterState struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	limit   rate.Limit
	burst   int
	trusted []netip.Prefix
	max     int
}

type limiterHandler struct {
	st   *limiterState
	next http.Handler
}

// rateLimit recebe o teto como parâmetro para que o teste possa exercitá-lo
// sem criar cem mil buckets.
func rateLimit(ctx context.Context, perMinute, burst int, trusted []netip.Prefix, max int) func(http.Handler) http.Handler {
	st := &limiterState{
		buckets: map[string]*bucket{},
		limit:   rate.Limit(float64(perMinute) / 60.0),
		burst:   burst,
		trusted: trusted,
		max:     max,
	}

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				st.mu.Lock()
				st.varrer()
				st.mu.Unlock()
			}
		}
	}()

	return func(next http.Handler) http.Handler {
		return &limiterHandler{st: st, next: next}
	}
}

func (h *limiterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	chave := chaveDe(ClientIP(r, h.st.trusted))

	h.st.mu.Lock()
	b, ok := h.st.buckets[chave]
	if !ok {
		h.st.abrirEspaco()
		b = &bucket{lim: rate.NewLimiter(h.st.limit, h.st.burst)}
		h.st.buckets[chave] = b
	}
	b.seen = time.Now()
	allow := b.lim.Allow()
	h.st.mu.Unlock()

	if !allow {
		apierr.WriteError(w, r, apierr.CodeRateLimited)
		return
	}
	h.next.ServeHTTP(w, r)
}

// varrer remove o que está parado há tempo demais. Chamar com mu travado.
func (s *limiterState) varrer() {
	for k, b := range s.buckets {
		if time.Since(b.seen) > 10*time.Minute {
			delete(s.buckets, k)
		}
	}
}

// abrirEspaco garante que cabe mais uma entrada. Chamar com mu travado.
//
// Primeiro tenta a varredura normal; se o mapa continuar cheio — o caso do
// ataque, em que todas as entradas são recentes — despeja a menos vista
// recentemente. O custo da varredura é O(max), e não O(origens observadas),
// que é justamente a diferença entre ter e não ter teto.
func (s *limiterState) abrirEspaco() {
	if len(s.buckets) < s.max {
		return
	}
	s.varrer()
	for len(s.buckets) >= s.max {
		var maisVelha string
		var quando time.Time
		for k, b := range s.buckets {
			if quando.IsZero() || b.seen.Before(quando) {
				maisVelha, quando = k, b.seen
			}
		}
		delete(s.buckets, maisVelha)
	}
}

// chaveDe agrupa IPv6 por /64, que é o bloco entregue a um único assinante.
// Um balde por ENDEREÇO daria ao atacante bilhões de baldes dentro da própria
// alocação dele; por /64, variar o endereço não compra nada.
func chaveDe(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

// quantosBuckets existe para o teste do teto: é a única forma de observar o
// tamanho do mapa sem exportá-lo.
func quantosBuckets(h http.Handler) int {
	lh, ok := h.(*limiterHandler)
	if !ok {
		return -1
	}
	lh.st.mu.Lock()
	defer lh.st.mu.Unlock()
	return len(lh.st.buckets)
}
