package prober

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
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
//
// C3 (Plano 10): excesso de saltos e esquema que não é http(s) no Location
// são erro de configuração do site, e não proteção de rede: saem como
// unknown ("erro de rede" no alerta), e não como blocked ("destino bloqueado
// pela proteção de rede"). O blocked fica para o IP recusado no dial.
func TestProbeRedirectLoopIsUnknown(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/", http.StatusFound)
	})
	if res := Probe(t.Context(), client, especificacao(srv.URL)); res.Status != StatusDown || res.ErrorKind != KindUnknown {
		t.Errorf("res = %+v, queria down/unknown depois de 3 saltos", res)
	}
}

func TestProbeRedirectToAnotherSchemeIsUnknown(t *testing.T) {
	for _, destino := range []string{"ftp://exemplo.com/arquivo", "mailto:contato@exemplo.com"} {
		srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, destino, http.StatusFound)
		})
		if res := Probe(t.Context(), client, especificacao(srv.URL)); res.Status != StatusDown || res.ErrorKind != KindUnknown {
			t.Errorf("redirect para %s: res = %+v, queria down/unknown", destino, res)
		}
	}
}

// A outra metade do C3: o salto para um endereço interno continua blocked.
// A outra porta do 127.0.0.1 não está liberada no cliente do teste.
func TestProbeRedirectToAnInternalAddressIsStillBlocked(t *testing.T) {
	interno := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(interno.Close)
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, interno.URL, http.StatusFound)
	})
	if res := Probe(t.Context(), client, especificacao(srv.URL)); res.Status != StatusDown || res.ErrorKind != KindBlocked {
		t.Errorf("res = %+v, queria down/blocked no salto para outro endereço interno", res)
	}
}

// C1 (Plano 10): uma resposta com cabeçalho acima de 64 KiB é down, sem que
// o sondador aloque o que o servidor quiser mandar. O tipo é unknown, o
// residual: o net/http não tem erro próprio para isso, só texto. Um
// cabeçalho grande, mas legítimo, passa.
func TestProbeResponseHeadersAboveTheCapAreDown(t *testing.T) {
	for _, c := range []struct {
		tamanho int
		down    bool
	}{{1 << 20, true}, {100 << 10, true}, {32 << 10, false}} {
		srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Enchimento", strings.Repeat("a", c.tamanho))
			w.WriteHeader(http.StatusOK)
		})
		res := Probe(t.Context(), client, especificacao(srv.URL))
		switch {
		case c.down && (res.Status != StatusDown || res.ErrorKind != KindUnknown || res.StatusCode != 0):
			t.Errorf("cabeçalho de %d bytes: res = %+v, queria down/unknown sem status", c.tamanho, res)
		case !c.down && res.Status != StatusUp:
			t.Errorf("cabeçalho de %d bytes: res = %+v, queria up", c.tamanho, res)
		}
	}
}

// C1: o transporte real também propaga o teto de cabeçalhos ao HTTP/2.
// Sabotagem: maxResponseHeaderBytes de 64 KiB para 2 MiB deixa o caso de
// 100 KiB up e reprova este teste. O caso pequeno prova sucesso com TLS
// validado, e cada caso observa o protocolo usado no servidor.
func TestProbeHTTP2ResponseHeadersAboveTheCapAreDown(t *testing.T) {
	for _, c := range []struct {
		nome    string
		tamanho int
		down    bool
	}{{"32_KiB_passam", 32 << 10, false}, {"100_KiB_excedem_o_teto", 100 << 10, true}} {
		t.Run(c.nome, func(t *testing.T) {
			protocolo := make(chan int, 1)
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				protocolo <- r.ProtoMajor
				w.Header().Set("X-Enchimento", strings.Repeat("a", c.tamanho))
				w.WriteHeader(http.StatusOK)
			}))
			srv.EnableHTTP2 = true
			srv.StartTLS()
			t.Cleanup(srv.Close)

			client := safedial.NewClientAllowing(0, enderecoDe(t, srv))
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("transporte de produção = %T, queria *http.Transport", client.Transport)
			}
			raizes := x509.NewCertPool()
			raizes.AddCert(srv.Certificate())
			transport.TLSClientConfig = &tls.Config{RootCAs: raizes}
			t.Cleanup(transport.CloseIdleConnections)

			res := Probe(t.Context(), client, especificacao(srv.URL))
			select {
			case major := <-protocolo:
				if major != 2 {
					t.Fatalf("servidor recebeu HTTP/%d, queria HTTP/2", major)
				}
			default:
				t.Fatal("o handler não recebeu a requisição; TLS ou bloqueio mascarou a prova do teto")
			}
			if c.down {
				if res.Status != StatusDown || res.ErrorKind != KindUnknown || res.StatusCode != 0 {
					t.Errorf("cabeçalho de %d bytes em HTTP/2: res = %+v, queria down/unknown sem status", c.tamanho, res)
				}
			} else if res.Status != StatusUp || res.ErrorKind != "" || res.StatusCode != http.StatusOK {
				t.Errorf("cabeçalho de %d bytes em HTTP/2: res = %+v, queria up/200/sem erro", c.tamanho, res)
			}
		})
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
	if !strings.HasPrefix(recebido.Get("User-Agent"), "StillOn/") {
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

// Um nome de rótulo único (sem ponto) é nome da rede interna: o DNS do
// Docker resolve db, caddy e app. Se o que existe saísse blocked (o IP é
// interno) e o que não existe saísse dns, o error_kind diria quais nomes há
// na rede. Os dois têm de sair blocked, sem chegar ao DNS. O localhost faz o
// papel do nome que existe: resolve para loopback em qualquer máquina.
func TestProbeSingleLabelHostIsBlockedWhetherItExistsOrNot(t *testing.T) {
	cases := []struct{ nome, url string }{
		{"nome que existe", "http://localhost/"},
		{"nome que existe em maiúsculas", "http://LOCALHOST:8080/"},
		{"nome que existe com ponto final", "http://localhost./"},
		{"nome que não existe", "http://naoexiste-stillon-x7q/"},
		{"nome que não existe com ponto final", "http://naoexiste-stillon-x7q./"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			res := Probe(t.Context(), safedial.NewClient(0), especificacao(c.url))
			if res.Status != StatusDown || res.ErrorKind != KindBlocked {
				t.Errorf("Probe(%q) = %+v, queria down/blocked", c.url, res)
			}
		})
	}
}

// O mesmo vale no meio de um redirecionamento: o salto para um nome de
// rótulo único não pode revelar se ele existe.
func TestProbeRedirectToASingleLabelHostIsBlocked(t *testing.T) {
	for _, destino := range []string{"http://localhost/", "http://naoexiste-stillon-x7q/"} {
		t.Run(destino, func(t *testing.T) {
			srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destino, http.StatusFound)
			})
			if res := Probe(t.Context(), client, especificacao(srv.URL)); res.Status != StatusDown || res.ErrorKind != KindBlocked {
				t.Errorf("salto para %q: res = %+v, queria down/blocked", destino, res)
			}
		})
	}
}

// segredos são headers de alvo que o net/http trata de jeitos diferentes:
// Authorization ele mesmo tira ao trocar de domínio, X-Token não.
func segredos() map[string]string {
	return map[string]string{"X-Token": "segredo", "Authorization": "Bearer segredo"}
}

// Spec §5.4: headers de alvo são segredo, e um redirect para outro
// host:porta não os leva. Duas portas do mesmo 127.0.0.1 contam como hosts
// diferentes.
func TestProbeRedirectToAnotherHostDropsTargetHeaders(t *testing.T) {
	naOrigem, noDestino := make(chan http.Header, 1), make(chan http.Header, 1)
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noDestino <- r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(destino.Close)
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		naOrigem <- r.Header.Clone()
		http.Redirect(w, r, destino.URL, http.StatusMovedPermanently)
	}))
	t.Cleanup(origem.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, origem), enderecoDe(t, destino))

	s := especificacao(origem.URL)
	s.Headers = segredos()
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Fatalf("res = %+v, queria up depois do redirect", res)
	}

	// A origem recebeu os segredos: sem isto, o teste passaria com uma sonda
	// que nunca os mandou.
	if h := <-naOrigem; h.Get("X-Token") != "segredo" || h.Get("Authorization") == "" {
		t.Fatalf("a origem não recebeu os headers do alvo: %v", h)
	}
	h := <-noDestino
	if v := h.Get("X-Token"); v != "" {
		t.Errorf("X-Token = %q chegou ao outro host", v)
	}
	if v := h.Get("Authorization"); v != "" {
		t.Errorf("Authorization = %q chegou ao outro host", v)
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "StillOn/") {
		t.Errorf("User-Agent = %q no outro host, queria o do monitor", h.Get("User-Agent"))
	}
}

// O outro lado: redirect para o MESMO host:porta mantém os headers. Sem
// este teste, uma remoção incondicional passaria no de cima.
func TestProbeRedirectWithinSameHostKeepsTargetHeaders(t *testing.T) {
	noDestino := make(chan http.Header, 1)
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/destino" {
			noDestino <- r.Header.Clone()
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/destino", http.StatusMovedPermanently)
	})

	s := especificacao(srv.URL)
	s.Headers = segredos()
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Fatalf("res = %+v, queria up depois do redirect", res)
	}
	h := <-noDestino
	if h.Get("X-Token") != "segredo" || h.Get("Authorization") != "Bearer segredo" {
		t.Errorf("headers no mesmo host = %v, queria X-Token e Authorization", h)
	}
}

// O net/http copia os headers da requisição ORIGINAL a cada salto. Uma
// cadeia origem → outro host → origem precisa continuar limpa na volta: o
// outro host é quem escolheu o destino.
func TestProbeRedirectBackToTheOriginStaysClean(t *testing.T) {
	naVolta := make(chan http.Header, 1)
	desvio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Query().Get("volta"), http.StatusFound)
	}))
	t.Cleanup(desvio.Close)
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/volta" {
			naVolta <- r.Header.Clone()
			w.WriteHeader(http.StatusOK)
			return
		}
		// r.Host, e não origem.URL: ler a variável daqui seria corrida.
		http.Redirect(w, r, desvio.URL+"/?volta=http://"+r.Host+"/volta", http.StatusFound)
	}))
	t.Cleanup(origem.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, origem), enderecoDe(t, desvio))

	s := especificacao(origem.URL)
	s.Headers = segredos()
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Fatalf("res = %+v, queria up depois de dois redirects", res)
	}
	if h := <-naVolta; h.Get("X-Token") != "" || h.Get("Authorization") != "" {
		t.Errorf("headers na volta à origem = %v, queria sem os do alvo", h)
	}
}

// O User-Agent também pode ser configurado pelo dono: não é exceção à
// proteção dos headers. Sabotagem: preservar o User-Agent em limparHeaders.
func TestProbeRedirectDropsCustomUserAgentThroughoutAnUnsafeChain(t *testing.T) {
	const sentinela = "credencial-publica-de-teste-no-user-agent"
	naOrigem, noDesvio, naVolta := make(chan string, 1), make(chan string, 1), make(chan string, 1)
	desvio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		noDesvio <- r.Header.Get("User-Agent")
		http.Redirect(w, r, r.URL.Query().Get("volta"), http.StatusFound)
	}))
	t.Cleanup(desvio.Close)
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/volta" {
			naVolta <- r.Header.Get("User-Agent")
			w.WriteHeader(http.StatusOK)
			return
		}
		naOrigem <- r.Header.Get("User-Agent")
		http.Redirect(w, r, desvio.URL+"/?volta=http://"+r.Host+"/volta", http.StatusFound)
	}))
	t.Cleanup(origem.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, origem), enderecoDe(t, desvio))
	s := especificacao(origem.URL)
	s.Headers = map[string]string{"User-Agent": sentinela}
	if res := Probe(t.Context(), client, s); res.Status != StatusUp {
		t.Fatalf("sonda = %+v, queria sucesso", res)
	}
	if recebido := <-naOrigem; recebido != sentinela {
		t.Fatalf("a origem recebeu %q, queria o valor personalizado", recebido)
	}
	for salto, recebido := range map[string]string{"outro destino": <-noDesvio, "volta à origem": <-naVolta} {
		if recebido == sentinela || !strings.HasPrefix(recebido, "StillOn/") {
			t.Errorf("%s recebeu User-Agent %q, queria somente identificação pública", salto, recebido)
		}
	}
}
