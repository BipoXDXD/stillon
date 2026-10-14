package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/BipoXDXD/stillon/internal/clock"
)

// TestRateLimitCapsTheNumberOfBuckets trava o item 30, que hoje é inerte
// APENAS por causa do item 21: com RemoteAddr cru atrás do proxy o mapa tem
// uma entrada só, e o atacante não consegue variar a chave. No instante em
// que o ClientIP passa a ler o X-Forwarded-For de um proxy confiável, ele
// recupera esse controle — e um mapa sem teto vira exaustão de memória.
// Por isso os dois saem juntos.
func TestRateLimitCapsTheNumberOfBuckets(t *testing.T) {
	const teto = 8
	h := rateLimit(t.Context(), clock.Real{}, porMinuto(60), 5, porIP(Proxies{}), teto)(http.HandlerFunc(
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
	h := rateLimit(t.Context(), clock.Real{}, porMinuto(60), 1, porIP(Proxies{}), 1000)(http.HandlerFunc(
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
	h := rateLimit(t.Context(), clock.Real{}, porMinuto(60), 5, porUsuario, 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("sem usuário no contexto deu %d, queria 401", rec.Code)
	}
}

// Apagar um balde é devolvê-lo cheio. Um limite de um token a cada 6
// minutos com burst 5 leva 30 minutos para encher; se a varredura o apagasse
// aos 10 minutos parado, o usuário ganharia 5 envios novos a cada 10
// minutos, o triplo do limite.
func TestSweepKeepsABucketUntilItCouldHaveRefilled(t *testing.T) {
	h := rateLimit(t.Context(), clock.Real{}, rate.Every(6*time.Minute), 5, porUsuario, 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	lh := h.(*limiterHandler)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/channels", nil)
	r = r.WithContext(context.WithValue(r.Context(), userKey{}, uuid.New()))
	for range 5 {
		h.ServeHTTP(httptest.NewRecorder(), r)
	}

	lh.st.mu.Lock()
	for _, e := range lh.st.buckets {
		e.Value.(*bucket).seen = time.Now().Add(-11 * time.Minute)
	}
	lh.st.varrer()
	n := len(lh.st.buckets)
	lh.st.mu.Unlock()
	if n != 1 {
		t.Fatal("o balde vazio foi apagado aos 11 minutos, antes de poder encher: o limite por hora vazaria")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("depois da varredura, a sexta requisição deu %d, queria 429", rec.Code)
	}
}

// A varredura apaga o balde parado além do ocioso e mantém o que ainda está
// dentro dele.
func TestSweepRemovesOnlyTheIdleBuckets(t *testing.T) {
	h := rateLimit(t.Context(), clock.Real{}, rate.Every(time.Hour), 1, porIP(Proxies{}), 1000)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	lh := h.(*limiterHandler)
	pedir := func(origem string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = origem + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	const parada, ativa = "198.51.100.1", "198.51.100.2"
	pedir(parada)
	pedir(ativa)

	lh.st.mu.Lock()
	lh.st.buckets[chaveDe(parada)].Value.(*bucket).seen = time.Now().Add(-lh.st.ocioso - time.Minute)
	lh.st.varrer()
	lh.st.mu.Unlock()

	if n := quantosBuckets(h); n != 1 {
		t.Fatalf("%d buckets depois da varredura, queria 1 (só o ativo)", n)
	}
	if code := pedir(ativa); code != http.StatusTooManyRequests {
		t.Errorf("o balde ativo foi varrido: deu %d, queria 429", code)
	}
}

// Com o mapa no teto, a origem nova despeja a MENOS USADA recentemente, e
// não a primeira que entrou: quem continua mandando requisições mantém o
// balde (e o limite dele). Com burst 1, o balde mantido responde 429 e o
// despejado volta cheio e responde 200.
func TestRateLimitFullMapEvictsTheLeastRecentlyUsedOrigin(t *testing.T) {
	const teto = 3
	h := rateLimit(t.Context(), clock.Real{}, rate.Every(time.Hour), 1, porIP(Proxies{}), teto)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	pedir := func(origem string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = origem + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	const a, b, c, d = "198.51.100.1", "198.51.100.2", "198.51.100.3", "198.51.100.4"

	for _, origem := range []string{a, b, c} {
		if code := pedir(origem); code != http.StatusOK {
			t.Fatalf("primeira requisição de %s deu %d, queria 200", origem, code)
		}
	}
	if code := pedir(a); code != http.StatusTooManyRequests {
		t.Fatalf("segunda requisição de %s deu %d, queria 429", a, code)
	}
	pedir(d) // mapa cheio: despeja b, a menos usada (a acabou de ser usada)

	if code := pedir(a); code != http.StatusTooManyRequests {
		t.Errorf("%s, usada depois de %s, perdeu o balde: deu %d, queria 429", a, b, code)
	}
	if code := pedir(b); code != http.StatusOK {
		t.Errorf("%s, a menos usada, continuou no mapa: deu %d, queria 200 de balde novo", b, code)
	}
	if code := pedir(d); code != http.StatusTooManyRequests {
		t.Errorf("%s, a mais recente, foi despejada: deu %d, queria 429", d, code)
	}
	if n := quantosBuckets(h); n != teto {
		t.Errorf("%d buckets no mapa, queria o teto %d", n, teto)
	}
}
