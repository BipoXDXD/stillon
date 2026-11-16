package prober

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// Estes testes trocam o net.DefaultResolver (resolverDeTeste) e por isso não
// usam t.Parallel.

const (
	hostSonda = "sonda.exemplo.test"
	hostOutro = "outro.exemplo.test"
)

var (
	folhaAte         = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	intermediarioAte = time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)
	folhaDoOutroAte  = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
)

// servidorTLS sobe um httptest com a cadeia de teste de host e devolve a raiz
// que a verifica.
func servidorTLS(t *testing.T, host string, folha time.Time, http2 bool, h http.HandlerFunc) (*httptest.Server, *x509.Certificate) {
	t.Helper()
	return servidorTLSComIntermediario(t, host, folha, intermediarioAte, http2, h)
}

// servidorTLSComIntermediario é o servidorTLS com a validade do intermediário
// escolhida pelo teste.
func servidorTLSComIntermediario(t *testing.T, host string, folha, intermediario time.Time, http2 bool, h http.HandlerFunc) (*httptest.Server, *x509.Certificate) {
	t.Helper()
	raiz, cert := cadeiaDeTeste(t, host, folha, intermediario)
	srv := httptest.NewUnstartedServer(h)
	srv.EnableHTTP2 = http2
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, raiz
}

func urlDe(t *testing.T, esquema, host string, srv *httptest.Server) string {
	t.Helper()
	return fmt.Sprintf("%s://%s:%d/", esquema, host, enderecoDe(t, srv).Port())
}

func responder200(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func redirecionarPara(destino string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destino, http.StatusFound) }
}

func conferirValidade(t *testing.T, res Result, quer time.Time) {
	t.Helper()
	if !res.CertNotAfter.Equal(quer) {
		t.Errorf("CertNotAfter = %v, queria %v (res = %+v)", res.CertNotAfter, quer, res)
	}
}

func conferirUp(t *testing.T, res Result) {
	t.Helper()
	if res.Status != StatusUp || res.ErrorKind != "" || res.StatusCode != http.StatusOK {
		t.Fatalf("res = %+v, queria up/200/sem erro", res)
	}
}

func TestProbeReadsTheRegisteredHostCertificate(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")
	srv, raiz := servidorTLS(t, hostSonda, folhaAte, false, responder200)
	client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

	conferirUp(t, res)
	conferirValidade(t, res, intermediarioAte)
}

// O aviso é sobre o host cadastrado: o certificado do destino de um redirect
// para outro host não conta, mesmo vencendo antes. Sabotagem: ler resp.TLS
// (o último salto) deixa CertNotAfter zerado,
// porque o handshake do último salto é do outro host.
func TestProbeIgnoresTheCertificateOfARedirectTarget(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")
	outro, raizOutro := servidorTLS(t, hostOutro, folhaDoOutroAte, false, responder200)
	sonda, raizSonda := servidorTLS(t, hostSonda, folhaAte, false, redirecionarPara(urlDe(t, "https", hostOutro, outro)))
	client := clienteComRaiz(t, raizSonda, enderecoDe(t, sonda), enderecoDe(t, outro))
	confiarTambem(t, client, raizOutro)

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, sonda)))

	conferirUp(t, res)
	conferirValidade(t, res, intermediarioAte)
}

func TestProbeOverHTTPHasNoCertificate(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")

	t.Run("sem salto não há leitura", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(responder200))
		t.Cleanup(srv.Close)
		raiz, _ := cadeiaDeTeste(t, hostSonda, folhaAte, intermediarioAte)
		client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

		res := Probe(t.Context(), client, especificacao(urlDe(t, "http", hostSonda, srv)))

		conferirUp(t, res)
		conferirValidade(t, res, time.Time{})
	})

	t.Run("salto para https do mesmo host lê", func(t *testing.T) {
		seguro, raiz := servidorTLS(t, hostSonda, folhaAte, false, responder200)
		aberto := httptest.NewServer(redirecionarPara(urlDe(t, "https", hostSonda, seguro)))
		t.Cleanup(aberto.Close)
		client := clienteComRaiz(t, raiz, enderecoDe(t, aberto), enderecoDe(t, seguro))

		res := Probe(t.Context(), client, especificacao(urlDe(t, "http", hostSonda, aberto)))

		conferirUp(t, res)
		conferirValidade(t, res, intermediarioAte)
	})
}

// IP literal não tem SNI: mesmo com o handshake verificado (SAN de IP), não há
// certificado do host a avaliar.
func TestProbeOnIPLiteralHasNoCertificate(t *testing.T) {
	srv, raiz := servidorTLS(t, "127.0.0.1", folhaAte, false, responder200)
	client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", "127.0.0.1", srv)))

	conferirUp(t, res)
	conferirValidade(t, res, time.Time{})
}

func TestProbeAcceptsTheTrailingDot(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")
	srv, raiz := servidorTLS(t, hostSonda, folhaAte, false, responder200)
	client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda+".", srv)))

	conferirUp(t, res)
	conferirValidade(t, res, intermediarioAte)
}

// Em HTTP/2 o trace também dispara, e em cada salto: o redirect para o mesmo
// host abre uma segunda conexão (sem keep-alive) e as duas passam pelo
// coletor. Os saltos são sequenciais. TestHandshakeCollectorConcurrentSnapshot
// exercita a escrita concorrente à leitura do coletor.
func TestTLSHandshakeObservedOverHTTP2(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")
	protocolos := make(chan int, 2)
	srv, raiz := servidorTLS(t, hostSonda, folhaAte, true, func(w http.ResponseWriter, r *http.Request) {
		protocolos <- r.ProtoMajor
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/fim", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

	conferirUp(t, res)
	for range 2 {
		select {
		case major := <-protocolos:
			if major != 2 {
				t.Fatalf("servidor recebeu HTTP/%d, queria HTTP/2", major)
			}
		default:
			t.Fatal("o handler não recebeu os dois saltos")
		}
	}
	conferirValidade(t, res, intermediarioAte)
}

// Certificado não verificado não influencia aviso.
func TestUnverifiedCertificateRecordsNothing(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")
	srv, _ := servidorTLS(t, hostSonda, folhaAte, false, responder200)
	raizAlheia, _ := cadeiaDeTeste(t, hostSonda, folhaAte, intermediarioAte)
	client := clienteComRaiz(t, raizAlheia, enderecoDe(t, srv))

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

	if res.Status != StatusDown || res.ErrorKind != KindTLS {
		t.Fatalf("res = %+v, queria down/tls", res)
	}
	conferirValidade(t, res, time.Time{})
}

// Ponta a ponta: um handshake que conclui sem verificar (InsecureSkipVerify,
// só neste teste) traz PeerCertificates e nenhuma VerifiedChains, e não pode
// virar leitura. Sabotagem: cair em PeerCertificates quando VerifiedChains
// vem vazio.
func TestPresentedButUnverifiedCertificateRecordsNothing(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")
	srv, _ := servidorTLS(t, hostSonda, folhaAte, false, responder200)
	client := safedial.NewClientAllowing(0, enderecoDe(t, srv))
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transporte de produção = %T, queria *http.Transport", client.Transport)
	}
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- handshake sem verificação, só no teste
	t.Cleanup(transport.CloseIdleConnections)

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

	conferirUp(t, res)
	conferirValidade(t, res, time.Time{})
}

// O transporte de teste só devolve o erro do prazo. O trace pertence ao
// request real de Probe e recebe a conclusão depois do retorno da sonda.
func TestProbeReturnsTimeoutBeforeLateHandshake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := especificacao("https://" + hostSonda + "/")
		var trace *httptrace.ClientTrace
		var observados []tls.ConnectionState
		observador := &httptrace.ClientTrace{
			TLSHandshakeDone: func(estado tls.ConnectionState, err error) {
				if err != nil {
					t.Errorf("callback recebeu erro: %v", err)
					return
				}
				observados = append(observados, estado)
			},
		}
		ctx := httptrace.WithClientTrace(t.Context(), observador)
		client := &http.Client{Transport: transporteFunc(func(req *http.Request) (*http.Response, error) {
			trace = httptrace.ContextClientTrace(req.Context())
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(2 * s.Timeout):
				return nil, fmt.Errorf("o contexto da sonda não expirou no prazo")
			}
		})}

		inicio := time.Now()
		res := Probe(ctx, client, s)
		if res.Status != StatusDown || res.ErrorKind != KindTimeout || res.StatusCode != 0 {
			t.Fatalf("res = %+v, queria down/timeout sem status HTTP", res)
		}
		if decorrido := time.Since(inicio); decorrido != s.Timeout {
			t.Errorf("a sonda retornou após %v, queria %v", decorrido, s.Timeout)
		}
		conferirValidade(t, res, time.Time{})
		if len(observados) != 0 {
			t.Fatalf("houve %d callbacks antes do retorno, queria zero", len(observados))
		}
		if trace == nil || trace == observador || trace.TLSHandshakeDone == nil {
			t.Fatal("o request não recebeu o trace de handshake de Probe")
		}

		folha := &x509.Certificate{DNSNames: []string{hostSonda}, NotAfter: folhaAte}
		estado := tls.ConnectionState{
			HandshakeComplete: true,
			ServerName:        hostSonda,
			VerifiedChains:    [][]*x509.Certificate{{folha}},
		}
		terminou := make(chan struct{})
		go func() {
			defer close(terminou)
			trace.TLSHandshakeDone(estado, nil)
		}()
		select {
		case <-terminou:
		case <-time.After(s.Timeout):
			t.Fatal("o callback tardio não terminou")
		}
		if len(observados) != 1 || observados[0].ServerName != hostSonda {
			t.Fatalf("callbacks após o retorno = %+v, queria um para %s", observados, hostSonda)
		}
	})
}

// Sabotagem: tirar os locks do coletor deve fazer o -race acusar a escrita do
// append e a leitura do slice. O sinal de partida precede os dois acessos
// sem ordená-los entre si. A espera pela escrita vem depois da leitura.
func TestHandshakeCollectorConcurrentSnapshot(t *testing.T) {
	trace, coletados := coletorDeHandshakes()
	estado := tls.ConnectionState{ServerName: hostSonda, HandshakeComplete: true}
	partir := make(chan struct{})
	terminou := make(chan struct{})
	go func() {
		defer close(terminou)
		<-partir
		trace.TLSHandshakeDone(estado, nil)
	}()
	close(partir)
	primeiraLeitura := coletados()
	select {
	case <-terminou:
	case <-time.After(5 * time.Second):
		t.Fatal("o callback concorrente não terminou")
	}

	if len(primeiraLeitura) > 1 {
		t.Errorf("a leitura concorrente viu %d handshakes, queria zero ou um", len(primeiraLeitura))
	}
	if estados := coletados(); len(estados) != 1 || estados[0].ServerName != hostSonda {
		t.Errorf("o coletor registrou %+v, queria um handshake para %s", estados, hostSonda)
	}
}

// Down por status ainda passou por um handshake verificado: a validade vale.
func TestDownByStatusStillReadsTheCertificate(t *testing.T) {
	resolverDeTeste(t, "127.0.0.1")
	srv, raiz := servidorTLS(t, hostSonda, folhaAte, false, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

	res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

	if res.Status != StatusDown || res.ErrorKind != KindHTTPStatus || res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("res = %+v, queria down/http_status/500", res)
	}
	conferirValidade(t, res, intermediarioAte)
}

// Os certificados que o dono vai ver
// no aviso (folha vencida, intermediário vencido com a folha válida e emitido
// para outro nome) falham a verificação no handshake: a sonda sai down/tls e
// não há leitura. Sabotagem: no coletor, guardar os UnverifiedCertificates do
// CertificateVerificationError com o host do GetConn como ServerName ("mostrar
// a data do vencido") faz os três casos lerem uma validade.
func TestInvalidCertificateRecordsNothing(t *testing.T) {
	vencidaEm := agoraDoTLS.Add(-24 * time.Hour)
	casos := []struct {
		nome, hostDoCertificado string
		folhaAte, interAte      time.Time
	}{
		{nome: "folha vencida", hostDoCertificado: hostSonda, folhaAte: vencidaEm, interAte: intermediarioAte},
		{nome: "intermediário vencido", hostDoCertificado: hostSonda, folhaAte: folhaAte, interAte: vencidaEm},
		{nome: "emitido para outro nome", hostDoCertificado: hostOutro, folhaAte: folhaAte, interAte: intermediarioAte},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			resolverDeTeste(t, "127.0.0.1")
			srv, raiz := servidorTLSComIntermediario(t, c.hostDoCertificado, c.folhaAte, c.interAte, false, responder200)
			client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

			res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

			if res.Status != StatusDown || res.ErrorKind != KindTLS {
				t.Fatalf("res = %+v, queria down/tls", res)
			}
			conferirValidade(t, res, time.Time{})
		})
	}
}

// ouvinteContado guarda, em ordem, o endereço remoto de cada conexão que o
// servidor aceitou. No TCP a discagem do cliente termina quando o kernel
// completa o three-way handshake, antes do Accept: a contagem crua lida logo
// depois do Probe pode perder uma conexão sem handshake TLS. Por isso a
// contagem passa por conexoesAntesDaSentinela.
type ouvinteContado struct {
	net.Listener
	mu      sync.Mutex
	remotos []string
	aceitou chan struct{}
}

func (o *ouvinteContado) Accept() (net.Conn, error) {
	conn, err := o.Listener.Accept()
	if err == nil {
		o.mu.Lock()
		o.remotos = append(o.remotos, conn.RemoteAddr().String())
		o.mu.Unlock()
		select {
		case o.aceitou <- struct{}{}:
		default:
		}
	}
	return conn, err
}

// conexoesAntesDaSentinela abre uma conexão sentinela e espera o servidor
// aceitá-la. A fila de accept é FIFO: toda conexão que a sonda completou antes
// de devolver o resultado é aceita antes da sentinela, com ou sem handshake
// TLS. Devolve quantas foram aceitas antes dela.
func (o *ouvinteContado) conexoesAntesDaSentinela(t *testing.T) int {
	t.Helper()
	sentinela, err := net.Dial("tcp", o.Addr().String())
	if err != nil {
		t.Fatalf("discar a sentinela: %v", err)
	}
	defer sentinela.Close()
	endereco := sentinela.LocalAddr().String()
	prazo := time.After(esperaMaxima)
	for {
		o.mu.Lock()
		i := slices.Index(o.remotos, endereco)
		o.mu.Unlock()
		if i >= 0 {
			return i
		}
		select {
		case <-o.aceitou:
		case <-prazo:
			t.Fatal("o servidor não aceitou a conexão sentinela")
		}
	}
}

// servidorTLSContado é o servidorTLS com o ouvinte que conta conexões.
func servidorTLSContado(t *testing.T, host string) (*httptest.Server, *x509.Certificate, *ouvinteContado) {
	t.Helper()
	raiz, cert := cadeiaDeTeste(t, host, folhaAte, intermediarioAte)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(responder200))
	ouvinte := &ouvinteContado{Listener: srv.Listener, aceitou: make(chan struct{}, 1)}
	srv.Listener = ouvinte
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, raiz, ouvinte
}

// A leitura do certificado vem do
// handshake que o transporte do safedial já faz, e de nenhuma outra conexão:
// um destino que a lista fixa bloqueia (aqui, o loopback não liberado) não
// recebe conexão nem gera leitura, e um destino liberado recebe exatamente a
// conexão da sonda. Sabotagem: depois da sonda, discar o host com
// tls.DialWithDialer e o TLSClientConfig do transporte para ler o certificado
// "de verdade" (a abordagem B da spec) faz o bloqueado ler a validade e o
// liberado receber duas conexões; só um net.Dial e Close no destino, sem
// handshake (o oráculo de porta aberta), também conta como conexão a mais.
func TestCertificateIsReadOnlyThroughTheSafedialConnection(t *testing.T) {
	t.Run("destino bloqueado não gera leitura", func(t *testing.T) {
		resolverDeTeste(t, "127.0.0.1")
		srv, raiz, ouvinte := servidorTLSContado(t, hostSonda)
		client := clienteComRaiz(t, raiz) // nenhum endereço liberado

		res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

		if res.Status != StatusDown || res.ErrorKind != KindBlocked {
			t.Fatalf("res = %+v, queria down/blocked", res)
		}
		conferirValidade(t, res, time.Time{})
		if n := ouvinte.conexoesAntesDaSentinela(t); n != 0 {
			t.Errorf("o servidor bloqueado aceitou %d conexões; queria nenhuma", n)
		}
	})

	t.Run("destino liberado recebe só a conexão da sonda", func(t *testing.T) {
		resolverDeTeste(t, "127.0.0.1")
		srv, raiz, ouvinte := servidorTLSContado(t, hostSonda)
		client := clienteComRaiz(t, raiz, enderecoDe(t, srv))

		res := Probe(t.Context(), client, especificacao(urlDe(t, "https", hostSonda, srv)))

		conferirUp(t, res)
		conferirValidade(t, res, intermediarioAte)
		if n := ouvinte.conexoesAntesDaSentinela(t); n != 1 {
			t.Errorf("o servidor aceitou %d conexões; queria 1, a da sonda", n)
		}
	})
}
