package prober

import (
	"math"
	"sync"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// Teto de requisições por host de destino, somando todos os alvos de todos os
// projetos que apontam para ele: sem isto, quem cadastra muitos alvos no mesmo
// host faz do sondador uma fonte de carga contra terceiros. Fica em memória: o
// sondador roda numa réplica só, e o pior caso de um restart é uma rajada a
// mais.
const (
	RequisicoesPorHostPorMinuto = 60
	RajadaPorHost               = 20
)

// podaDosBaldes é de quanto em quanto tempo os baldes parados saem do mapa.
const podaDosBaldes = time.Minute

// tetoPorHost é um balde de fichas por host canônico (safedial.DialedHost):
// Exemplo.COM e exemplo.com dividem o balde.
type tetoPorHost struct {
	mu        sync.Mutex
	porSeg    float64 // fichas que chegam por segundo
	rajada    float64
	porHost   map[string]*baldeDeHost
	ultimaPod time.Time
}

type baldeDeHost struct {
	fichas float64
	em     time.Time // instante a que fichas se refere
}

func novoTetoPorHost(porMinuto, rajada int) *tetoPorHost {
	return &tetoPorHost{
		porSeg:  float64(porMinuto) / 60,
		rajada:  float64(rajada),
		porHost: map[string]*baldeDeHost{},
	}
}

// Reservar gasta uma ficha do host em agora; sem ficha, devolve a espera até a
// próxima e não gasta nada. Host que não canonicaliza segue sem teto: a sonda
// o recusa adiante, como antes do teto.
func (t *tetoPorHost) Reservar(host string, agora time.Time) (espera time.Duration, ok bool) {
	host, canonico := safedial.DialedHost(host)
	if !canonico || host == "" {
		return 0, true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.podar(agora)
	b, existe := t.porHost[host]
	if !existe {
		b = &baldeDeHost{fichas: t.rajada, em: agora}
		t.porHost[host] = b
	}
	b.fichas = t.fichasEm(b, agora)
	if agora.After(b.em) {
		b.em = agora
	}
	if b.fichas >= 1 {
		b.fichas--
		return 0, true
	}
	falta := (1 - b.fichas) / t.porSeg
	return time.Duration(math.Ceil(falta * float64(time.Second))), false
}

// fichasEm é o saldo do balde em agora, com o reabastecimento desde a última
// conta e o teto da rajada. Relógio que volta não tira fichas.
func (t *tetoPorHost) fichasEm(b *baldeDeHost, agora time.Time) float64 {
	passou := agora.Sub(b.em)
	if passou <= 0 {
		return b.fichas
	}
	return math.Min(t.rajada, b.fichas+passou.Seconds()*t.porSeg)
}

// podar tira, no máximo uma vez por podaDosBaldes, os baldes que já estariam
// cheios: recriá-los cheios dá o mesmo resultado, e o mapa não cresce com
// todo host que já passou pelo sondador.
func (t *tetoPorHost) podar(agora time.Time) {
	if agora.Sub(t.ultimaPod) < podaDosBaldes {
		return
	}
	t.ultimaPod = agora
	for host, b := range t.porHost {
		if t.fichasEm(b, agora) >= t.rajada {
			delete(t.porHost, host)
		}
	}
}
