package prober

import (
	"net"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// mudo aceita o TCP e nunca responde ao ClientHello, nem fecha. Conta as
// conexões que o cliente ainda mantém abertas: cada uma sai da conta quando
// o Read do lado do servidor vê o cliente fechar.
type mudo struct {
	l       net.Listener
	abertas atomic.Int64
	mu      sync.Mutex
	conns   []net.Conn
}

func novoMudo(t *testing.T) *mudo {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mudo{l: l}
	go m.aceitar()
	t.Cleanup(m.fechar)
	return m
}

func (m *mudo) aceitar() {
	for {
		c, err := m.l.Accept()
		if err != nil {
			return
		}
		m.abertas.Add(1)
		m.mu.Lock()
		m.conns = append(m.conns, c)
		m.mu.Unlock()
		go func() {
			defer m.abertas.Add(-1)
			buf := make([]byte, 4096)
			for {
				if _, err := c.Read(buf); err != nil {
					return
				}
			}
		}()
	}
}

func (m *mudo) fechar() {
	m.l.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		c.Close()
	}
}

// esperarAte devolve true se cond ficar verdadeira dentro do prazo.
func esperarAte(prazo time.Duration, cond func() bool) bool {
	fim := time.Now().Add(prazo)
	for time.Now().Before(fim) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// Um alvo https que aceita o TCP e fica mudo no handshake não pode prender,
// depois de a sonda desistir, nem o socket nem a goroutine do dial.
func TestProbeLiberaConexaoDeHandshakeMudo(t *testing.T) {
	const sondas = 20
	m := novoMudo(t)
	ap := netip.MustParseAddrPort(m.l.Addr().String())
	client := safedial.NewClientAllowing(0, ap)
	t.Cleanup(client.CloseIdleConnections)

	antes := runtime.NumGoroutine()
	s := especificacao("https://" + ap.String() + "/")
	s.Timeout = 100 * time.Millisecond

	var wg sync.WaitGroup
	for range sondas {
		wg.Go(func() {
			if res := Probe(t.Context(), client, s); res.Status != StatusDown {
				t.Errorf("res = %+v, queria down", res)
			}
		})
	}
	wg.Wait()

	if !esperarAte(2*time.Second, func() bool { return m.abertas.Load() == 0 }) {
		t.Errorf("depois de %d sondas e 2 s, %d conexões seguem abertas no alvo (goroutines: %d antes, %d depois)",
			sondas, m.abertas.Load(), antes, runtime.NumGoroutine())
	}
	t.Logf("conexões abertas no alvo: %d; goroutines: %d antes, %d depois", m.abertas.Load(), antes, runtime.NumGoroutine())
}
