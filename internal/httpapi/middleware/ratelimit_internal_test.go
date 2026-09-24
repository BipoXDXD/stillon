package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRateLimitCapsTheNumberOfBuckets trava o item 30, que hoje é inerte
// APENAS por causa do item 21: com RemoteAddr cru atrás do proxy o mapa tem
// uma entrada só, e o atacante não consegue variar a chave. No instante em
// que o ClientIP passa a ler o X-Forwarded-For de um proxy confiável, ele
// recupera esse controle — e um mapa sem teto vira exaustão de memória.
// Por isso os dois saem juntos.
func TestRateLimitCapsTheNumberOfBuckets(t *testing.T) {
	const teto = 8
	h := rateLimit(t.Context(), 60, 5, porIP(nil), teto)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	for i := range 500 {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:1234", i%256)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	if n := quantosBuckets(h); n > teto {
		t.Errorf("%d buckets no mapa com teto de %d: o mapa cresce sem limite", n, teto)
	}
}

// TestIPv6ClientsShareABucketPerSlash64 corta o vetor na raiz: um /64 é o
// bloco que um único assinante recebe, então dar um balde por endereço
// entrega ao atacante bilhões de baldes de graça.
func TestIPv6ClientsShareABucketPerSlash64(t *testing.T) {
	h := rateLimit(t.Context(), 60, 1, porIP(nil), 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	var negados int
	for i := range 20 {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = fmt.Sprintf("[2001:db8:1:1::%x]:1234", i+1)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			negados++
		}
	}
	if negados == 0 {
		t.Error("20 endereços do mesmo /64 passaram todos: cada endereço ganhou o próprio balde")
	}
	if n := quantosBuckets(h); n != 1 {
		t.Errorf("%d buckets para um único /64, queria 1", n)
	}
}

// O limitador por usuário precisa falhar FECHADO. Montado por engano sem
// Authn na frente, ele não encontra usuário no contexto; cair num balde
// anônimo compartilhado seria silencioso. A requisição tem de ser recusada.
func TestRateLimitUserRefusesRequestsWithoutUser(t *testing.T) {
	h := rateLimit(t.Context(), 60, 5, porUsuario, 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("sem usuário no contexto deu %d, queria 401", rec.Code)
	}
}
