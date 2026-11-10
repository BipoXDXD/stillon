package prober

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
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
		t.Errorf("depois de %d sondas e 2 s, %d conexões seguem abertas no alvo", sondas, m.abertas.Load())
	}
	// Cada sonda presa deixava duas goroutines no cliente (dial e handshake).
	// A folga cobre goroutines do runtime e do testing que vêm e vão.
	if !esperarAte(2*time.Second, func() bool { return runtime.NumGoroutine() <= antes+folgaDeGoroutines }) {
		t.Errorf("depois de %d sondas e 2 s, goroutines: %d antes, %d depois", sondas, antes, runtime.NumGoroutine())
	}
}

const folgaDeGoroutines = 2

// Um alvo mudo não pode atrapalhar a sonda de outro alvo. Um transporte
// compartilhado com CloseIdleConnections a cada falha fechava a conexão
// HTTP/2 que outra sonda acabara de abrir e ainda não usara (o net/http a
// põe no pool antes do primeiro stream): a sonda saudável discava de novo,
// com latência dobrada. Cada sonda saudável deve abrir exatamente uma conexão.
func TestProbeMudoNaoAtrapalhaSondaHTTP2Saudavel(t *testing.T) {
	const sondasSaudaveis = 1000
	const sondadoresDoMudo = 8

	var conexoes atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("servidor recebeu HTTP/%d, queria HTTP/2", r.ProtoMajor)
		}
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.Config.ConnState = func(_ net.Conn, estado http.ConnState) {
		if estado == http.StateNew {
			conexoes.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	m := novoMudo(t)
	mudo := netip.MustParseAddrPort(m.l.Addr().String())
	client := safedial.NewClientAllowing(0, enderecoDe(t, srv), mudo)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transporte de produção = %T, queria *http.Transport", client.Transport)
	}
	raizes := x509.NewCertPool()
	raizes.AddCert(srv.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: raizes}
	t.Cleanup(transport.CloseIdleConnections)

	especMudo := especificacao("https://" + mudo.String() + "/")
	especMudo.Timeout = 5 * time.Millisecond
	parar := make(chan struct{})
	var wg sync.WaitGroup
	for range sondadoresDoMudo {
		wg.Go(func() {
			for {
				select {
				case <-parar:
					return
				default:
					Probe(t.Context(), client, especMudo)
				}
			}
		})
	}

	for i := range sondasSaudaveis {
		if res := Probe(t.Context(), client, especificacao(srv.URL)); res.Status != StatusUp {
			t.Errorf("sonda saudável %d: res = %+v, queria up", i, res)
		}
	}
	close(parar)
	wg.Wait()

	if got := conexoes.Load(); got != sondasSaudaveis {
		t.Errorf("%d sondas saudáveis abriram %d conexões, queria uma por sonda", sondasSaudaveis, got)
	}
}
