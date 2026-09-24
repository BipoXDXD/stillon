package prober

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

func enderecoDe(t *testing.T, srv *httptest.Server) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

// servidor sobe um httptest liberado no safedial e devolve o cliente que o
// alcança. Nenhum outro endereço fica liberado.
func servidor(t *testing.T, h http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, safedial.NewClientAllowing(0, enderecoDe(t, srv))
}

func especificacao(url string) Spec {
	return Spec{URL: url, Method: http.MethodGet, Timeout: 2 * time.Second, ExpectedStatus: http.StatusOK}
}

func TestProbeUp(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	res := Probe(t.Context(), client, especificacao(srv.URL))
	if res.Status != StatusUp || res.StatusCode != 200 || res.ErrorKind != "" {
		t.Errorf("res = %+v, queria up/200/sem erro", res)
	}
}

func TestProbeUnexpectedStatusIsDown(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	res := Probe(t.Context(), client, especificacao(srv.URL))
	if res.Status != StatusDown || res.ErrorKind != KindHTTPStatus || res.StatusCode != 500 {
		t.Errorf("res = %+v, queria down/http_status/500", res)
	}
}

func TestProbeExpectedStatusOtherThan200(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s := especificacao(srv.URL)
	s.ExpectedStatus = http.StatusNoContent
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Errorf("res = %+v, queria up", res)
	}
}

// Spec §11.5: sucesso lento é degraded; limiar zero desliga a classificação.
func TestProbeDegradedAboveThreshold(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	s := especificacao(srv.URL)
	s.DegradedThreshold = 50 * time.Millisecond
	if res := Probe(t.Context(), client, s); res.Status != StatusDegraded || res.ErrorKind != "" {
		t.Errorf("com limiar de 50ms: %+v, queria degraded sem erro", res)
	}
	s.DegradedThreshold = 0
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Errorf("com limiar desligado: %+v, queria up", res)
	}
}

// Os handlers que "nunca respondem" esperam no contexto da requisição ou em
// parar. O close(parar) é registrado DEPOIS de servidor(), e cleanups rodam
// em ordem inversa: o canal fecha antes do srv.Close, que senão ficaria
// preso esperando o handler.
func TestProbeTimeoutWaitingForHeaders(t *testing.T) {
	parar := make(chan struct{})
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-parar:
		}
	})
	t.Cleanup(func() { close(parar) })

	s := especificacao(srv.URL)
	s.Timeout = 200 * time.Millisecond
	if res := Probe(t.Context(), client, s); res.Status != StatusDown || res.ErrorKind != KindTimeout {
		t.Errorf("res = %+v, queria down/timeout", res)
	}
}

// O prazo vale também para o corpo: um servidor que manda o cabeçalho e
// para prenderia a sonda para sempre.
func TestProbeTimeoutWhileReadingBody(t *testing.T) {
	parar := make(chan struct{})
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-parar:
		}
	})
	t.Cleanup(func() { close(parar) })

	s := especificacao(srv.URL)
	s.Timeout = 200 * time.Millisecond
	if res := Probe(t.Context(), client, s); res.Status != StatusDown || res.ErrorKind != KindTimeout {
		t.Errorf("res = %+v, queria down/timeout", res)
	}
}

// D2 e spec §5.1: um corpo sem fim é lido até 64 KiB e descartado, e a sonda
// termina bem. Sem o limite, ela leria até o prazo estourar e daria timeout.
func TestProbeCapsAnEndlessBodyWithoutFailing(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		bloco := []byte(strings.Repeat("x", 32<<10))
		for {
			if _, err := w.Write(bloco); err != nil {
				return
			}
		}
	})
	s := especificacao(srv.URL)
	s.Timeout = 3 * time.Second
	res := Probe(t.Context(), client, s)
	if res.Status != StatusUp {
		t.Fatalf("res = %+v, queria up: o corpo não foi limitado", res)
	}
	if res.Latency >= s.Timeout {
		t.Errorf("a sonda levou %v, o prazo inteiro", res.Latency)
	}
}

func TestProbeFollowsRedirectWithinLimit(t *testing.T) {
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(destino.Close)
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destino.URL, http.StatusFound)
	}))
	t.Cleanup(origem.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, origem), enderecoDe(t, destino))

	if res := Probe(t.Context(), client, especificacao(origem.URL)); res.Status != StatusUp {
		t.Errorf("res = %+v, queria up depois de um redirect", res)
	}
}

// O handler redireciona para o próprio Host da requisição. Ler srv.URL de
// dentro dele seria corrida de dados: a variável é escrita pela goroutine do
// teste, e o detector não enxerga a rede como sincronização.
func TestProbeRedirectLoopIsBlocked(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/", http.StatusFound)
	})
	if res := Probe(t.Context(), client, especificacao(srv.URL)); res.ErrorKind != KindBlocked {
		t.Errorf("res = %+v, queria blocked depois de 3 saltos", res)
	}
}

func TestProbeSendsConfiguredHeadersAndUserAgent(t *testing.T) {
	// Canal, e não variável: o handler roda em outra goroutine, e o detector
	// de corrida não conta a resposta HTTP como sincronização.
	cabecalhos := make(chan http.Header, 1)
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		cabecalhos <- r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	})
	s := especificacao(srv.URL)
	s.Headers = map[string]string{"Authorization": "Bearer abc"}
	Probe(t.Context(), client, s)
	recebido := <-cabecalhos

	if recebido.Get("Authorization") != "Bearer abc" {
		t.Errorf("Authorization = %q", recebido.Get("Authorization"))
	}
	if !strings.HasPrefix(recebido.Get("User-Agent"), "Vigilume/") {
		t.Errorf("User-Agent = %q", recebido.Get("User-Agent"))
	}
}

func TestProbeConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort(l.Addr().String())
	l.Close()

	res := Probe(t.Context(), safedial.NewClientAllowing(0, ap), especificacao("http://"+ap.String()))
	if res.Status != StatusDown || res.ErrorKind != KindConnRefused {
		t.Errorf("res = %+v, queria down/conn_refused", res)
	}
}

func TestProbeUntrustedCertificateIsTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, srv))

	if res := Probe(t.Context(), client, especificacao(srv.URL)); res.ErrorKind != KindTLS {
		t.Errorf("res = %+v, queria tls", res)
	}
}

// O cliente de produção bloqueia o httptest, que escuta em loopback.
func TestProbeWithProductionClientIsBlocked(t *testing.T) {
	srv, _ := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	res := Probe(t.Context(), safedial.NewClient(0), especificacao(srv.URL))
	if res.Status != StatusDown || res.ErrorKind != KindBlocked {
		t.Errorf("res = %+v, queria down/blocked", res)
	}
}
