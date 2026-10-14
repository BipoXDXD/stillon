package middleware

import (
	"container/list"
	"context"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/BipoXDXD/stillon/internal/apierr"
	"github.com/BipoXDXD/stillon/internal/clock"
)

// maxBuckets limita quantas origens distintas o limitador acompanha ao mesmo
// tempo. Sem teto, o mapa é um vetor de exaustão de memória: cada origem
// inédita cria uma entrada que vive no mínimo 10 minutos, e um /64 de IPv6
// oferece endereços de graça. Ver o comentário de chaveDe sobre por que o
// agrupamento por /64 é a metade barata dessa defesa.
const maxBuckets = 100_000

// chaveFn devolve a chave do balde. O segundo retorno é false quando a
// requisição não traz a identidade que o limitador espera.
type chaveFn func(*http.Request) (string, bool)

// RateLimit aplica token bucket por cliente (spec §5.7). O relógio clk decide
// quando as fichas voltam e quando um balde está parado; os testes passam um
// relógio falso para que a recarga não dependa da velocidade da máquina.
//
// A varredura recebe ctx para poder parar. time.Tick nunca é coletado pelo
// GC — não há como cancelá-lo — então a goroutine de limpeza precisaria de
// um jeito explícito de terminar; amarrar isso ao contexto do servidor faz
// a goroutine morrer no shutdown gracioso junto com o resto, em vez de
// vazar para sempre (R32).
func RateLimit(ctx context.Context, clk clock.Clock, perMinute, burst int, px Proxies) func(http.Handler) http.Handler {
	return rateLimit(ctx, clk, porMinuto(perMinute), burst, porIP(px), maxBuckets)
}

// RateLimitUser limita por usuário autenticado, a segunda camada da spec
// §5.7. Tem de ficar DEPOIS de Authn na cadeia: é Authn que põe o usuário no
// contexto. O limite por IP continua valendo por fora; este existe porque
// um usuário com token válido, atrás de muitos IPs, escaparia do outro.
func RateLimitUser(ctx context.Context, clk clock.Clock, perMinute, burst int) func(http.Handler) http.Handler {
	return rateLimit(ctx, clk, porMinuto(perMinute), burst, porUsuario, maxBuckets)
}

// RateLimitUserEvery limita por usuário com um token a cada every. É a
// camada das rotas que mandam mensagem para fora (spec §5.8): criar canal e
// reenviar verificação custam um e-mail ou um POST de webhook, e o limite
// por minuto das rotas comuns faria do serviço um relay.
func RateLimitUserEvery(ctx context.Context, clk clock.Clock, every time.Duration, burst int) func(http.Handler) http.Handler {
	return rateLimit(ctx, clk, rate.Every(every), burst, porUsuario, maxBuckets)
}

func porMinuto(n int) rate.Limit { return rate.Limit(float64(n) / 60.0) }

func porIP(px Proxies) chaveFn {
	return func(r *http.Request) (string, bool) {
		return chaveDe(ClientIP(r, px)), true
	}
}

// porUsuario falha FECHADO: sem usuário no contexto, a requisição é
// recusada, em vez de cair num balde anônimo que todos dividiriam. O
// prefixo "u:" impede que uma chave de usuário colida com uma de IP.
func porUsuario(r *http.Request) (string, bool) {
	id, ok := UserID(r.Context())
	if !ok {
		return "", false
	}
	return "u:" + id.String(), true
}

type bucket struct {
	chave string
	lim   *rate.Limiter
	seen  time.Time
}

// limiterState guarda os baldes num LRU: o mapa acha o balde pela chave, e a
// lista os ordena por uso, o mais recente na frente. Como todo uso move o
// balde para a frente com seen = agora, a lista também fica em ordem de seen,
// e o fundo é sempre o mais parado. Despejar e varrer começam pelo fundo e
// param no primeiro que fica: cada balde é removido uma vez só, então o custo
// por requisição é O(1) amortizado, mesmo com o mapa cheio sob ataque.
type limiterState struct {
	mu      sync.Mutex
	buckets map[string]*list.Element
	uso     *list.List
	limit   rate.Limit
	burst   int
	chave   chaveFn
	max     int
	clk     clock.Clock
	// ocioso é quanto um balde parado sobrevive à varredura.
	ocioso time.Duration
}

type limiterHandler struct {
	st   *limiterState
	next http.Handler
}

// ociosoMinimo é o tempo mínimo que um balde parado sobrevive à varredura.
const ociosoMinimo = 10 * time.Minute

// rateLimit recebe o teto como parâmetro para que o teste possa exercitá-lo
// sem criar cem mil buckets.
func rateLimit(ctx context.Context, clk clock.Clock, limite rate.Limit, burst int, chave chaveFn, teto int) func(http.Handler) http.Handler {
	st := &limiterState{
		buckets: map[string]*list.Element{},
		uso:     list.New(),
		limit:   limite,
		burst:   burst,
		chave:   chave,
		max:     teto,
		clk:     clk,
		// Apagar um balde é devolvê-lo cheio. Isso só é inofensivo depois
		// de ele ter tido tempo de encher sozinho: com um token a cada 6
		// minutos e burst 5, são 30 minutos. Varrer aos 10 daria 5 envios
		// novos a cada 10 minutos, o triplo do limite.
		ocioso: max(ociosoMinimo, tempoParaEncher(limite, burst)),
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
	chave, ok := h.st.chave(r)
	if !ok {
		apierr.WriteError(w, r, apierr.CodeUnauthorized)
		return
	}

	h.st.mu.Lock()
	b := h.st.usar(chave)
	espera, allow := reservar(b.lim, h.st.clk.Now())
	h.st.mu.Unlock()

	if !allow {
		// Retry-After diz ao cliente legítimo quanto esperar. Sem prazo
		// conhecido (taxa zero), o cabeçalho fica de fora.
		if espera > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(espera.Seconds()))))
		}
		apierr.WriteError(w, r, apierr.CodeRateLimited)
		return
	}
	h.next.ServeHTTP(w, r)
}

// reservar é o Allow que também diz quanto falta para o próximo token. A
// reserva recusada é devolvida (Cancel): recusar não pode gastar o token que
// o cliente receberia ao respeitar o Retry-After.
func reservar(lim *rate.Limiter, agora time.Time) (time.Duration, bool) {
	res := lim.ReserveN(agora, 1)
	if !res.OK() {
		return 0, false
	}
	espera := res.DelayFrom(agora)
	if espera > 0 {
		res.CancelAt(agora)
		return espera, false
	}
	return 0, true
}

// usar devolve o balde da chave, criado se preciso, marcado como o mais
// recente. Chamar com mu travado.
func (s *limiterState) usar(chave string) *bucket {
	if e, ok := s.buckets[chave]; ok {
		b := e.Value.(*bucket)
		b.seen = s.clk.Now()
		s.uso.MoveToFront(e)
		return b
	}
	s.abrirEspaco()
	b := &bucket{chave: chave, lim: rate.NewLimiter(s.limit, s.burst), seen: s.clk.Now()}
	s.buckets[chave] = s.uso.PushFront(b)
	return b
}

// varrer remove o que está parado há tempo demais. Chamar com mu travado.
func (s *limiterState) varrer() {
	agora := s.clk.Now()
	for e := s.uso.Back(); e != nil && agora.Sub(e.Value.(*bucket).seen) > s.ocioso; e = s.uso.Back() {
		s.despejar(e)
	}
}

func (s *limiterState) despejar(e *list.Element) {
	delete(s.buckets, s.uso.Remove(e).(*bucket).chave)
}

// tempoParaEncher é quanto um balde vazio leva para voltar ao burst.
func tempoParaEncher(limite rate.Limit, burst int) time.Duration {
	if limite <= 0 || limite == rate.Inf {
		return 0
	}
	return time.Duration(float64(burst) / float64(limite) * float64(time.Second))
}

// abrirEspaco garante que cabe mais uma entrada. Chamar com mu travado.
//
// Primeiro tenta a varredura normal; se o mapa continuar cheio — o caso do
// ataque, em que todas as entradas são recentes — despeja a menos usada
// recentemente, que é o fundo da lista.
func (s *limiterState) abrirEspaco() {
	if len(s.buckets) < s.max {
		return
	}
	s.varrer()
	for len(s.buckets) >= s.max {
		s.despejar(s.uso.Back())
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
