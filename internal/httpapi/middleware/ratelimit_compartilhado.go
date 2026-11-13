package middleware

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/observability"
)

// Orcamentos guarda os baldes por chave fora do handler. Em produção é o
// Postgres (storage.OrcamentosPorIP): réplicas da API dividem o mesmo
// orçamento por origem. A versão em memória (NovosOrcamentosEmMemoria) só serve
// a um processo único e aos testes sem banco.
//
// Gastar tira uma ficha do balde (balde, chave), que recebe uma ficha a cada
// aCada até rajada. Sem ficha, devolve false e quanto falta para a próxima, e
// nada é gasto. Os tipos são só da biblioteca padrão: o storage, que
// implementa, não pode importar este pacote (middleware → auth → storage).
type Orcamentos interface {
	Gastar(ctx context.Context, balde string, chave []byte, aCada time.Duration, rajada int) (time.Duration, bool, error)
}

// BaldeIP é um orçamento por origem com nome próprio: baldes de nomes
// diferentes não dividem fichas.
type BaldeIP struct {
	Nome      string
	PorMinuto int
	Rajada    int
}

// RateLimitIPCompartilhado limita por IP como RateLimit, com o estado em o.
// A chave gravada é o HMAC da origem (agrupada por chaveDe) com chaveIP, a
// IP_HASH_KEY: o IP não sai do processo nem vai para o log.
//
// Falha FECHADO: sem resposta do orçamento, a requisição recebe o erro interno
// da API, como qualquer outro caminho que depende do banco (e o login depende
// dele de qualquer jeito). Abrir aqui daria tentativas ilimitadas a quem
// derrubasse ou atrasasse o banco.
func RateLimitIPCompartilhado(o Orcamentos, b BaldeIP, px Proxies, chaveIP []byte) func(http.Handler) http.Handler {
	if len(chaveIP) < crypto.KeySize {
		panic("middleware: RateLimitIPCompartilhado exige chaveIP de ao menos 32 bytes")
	}
	if b.Nome == "" || b.PorMinuto <= 0 || b.Rajada <= 0 {
		panic("middleware: BaldeIP sem nome, taxa ou rajada positiva")
	}
	aCada := time.Minute / time.Duration(b.PorMinuto)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chave := crypto.HashIP(chaveIP, chaveDe(ClientIP(r, px)))
			espera, permitido, err := o.Gastar(r.Context(), b.Nome, chave, aCada, b.Rajada)
			if err != nil {
				slog.Error("limite por IP", "balde", b.Nome, "erro", err, "trace_id", observability.TraceID(r.Context()))
				apierr.WriteError(w, r, apierr.CodeInternal)
				return
			}
			if !permitido {
				recusar(w, r, espera)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// recusar é o 429 com Retry-After em segundos inteiros, arredondado para cima.
// Sem prazo conhecido, o cabeçalho fica de fora.
func recusar(w http.ResponseWriter, r *http.Request, espera time.Duration) {
	if espera > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(espera.Seconds()))))
	}
	apierr.WriteError(w, r, apierr.CodeRateLimited)
}

// orcamentosEmMemoria é um limiterState por balde, criado no primeiro uso.
type orcamentosEmMemoria struct {
	ctx    context.Context
	clk    clock.Clock
	mu     sync.Mutex
	baldes map[string]*limiterState
}

// NovosOrcamentosEmMemoria guarda os baldes na memória deste processo: cada
// réplica teria o seu orçamento. ctx encerra as varreduras, como em RateLimit.
func NovosOrcamentosEmMemoria(ctx context.Context, clk clock.Clock) Orcamentos {
	return &orcamentosEmMemoria{ctx: ctx, clk: clk, baldes: map[string]*limiterState{}}
}

func (o *orcamentosEmMemoria) Gastar(_ context.Context, balde string, chave []byte, aCada time.Duration, rajada int) (time.Duration, bool, error) {
	st := o.estado(balde, aCada, rajada)
	st.mu.Lock()
	defer st.mu.Unlock()
	espera, permitido := reservar(st.usar(string(chave)).lim, st.clk.Now())
	return espera, permitido, nil
}

func (o *orcamentosEmMemoria) estado(balde string, aCada time.Duration, rajada int) *limiterState {
	o.mu.Lock()
	defer o.mu.Unlock()
	st, ok := o.baldes[balde]
	if !ok {
		st = novoEstado(o.ctx, o.clk, rate.Every(aCada), rajada, nil, maxBuckets)
		o.baldes[balde] = st
	}
	return st
}
