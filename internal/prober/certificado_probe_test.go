package prober

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	raiz, cert := cadeiaDeTeste(t, host, folha, intermediarioAte)
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

// Em HTTP/2 o handshake também passa pelo trace. O salto para o mesmo host
// abre uma segunda conexão (sem keep-alive), discada em outra goroutine: é o
// caso em que o coletor recebe handshakes concorrentes ao Probe.
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

// T3: certificado não verificado não influencia aviso.
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
