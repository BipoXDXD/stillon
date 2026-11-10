package prober

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
	"github.com/BipoXDXD/stillon/internal/targets"
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

// Result não tem campo para o corpo nem para mensagem de erro (spec §4.2,
// §5.2): só o resultado sai de Probe. Campo novo entra aqui de propósito.
func TestResultHasNoRoomForTheBody(t *testing.T) {
	tipo := reflect.TypeFor[Result]()
	campos := make([]string, 0, tipo.NumField())
	for campo := range tipo.Fields() {
		campos = append(campos, campo.Name+" "+campo.Type.String())
	}
	quer := []string{
		"Status prober.Status",
		"Latency time.Duration",
		"StatusCode int",
		"ErrorKind prober.ErrorKind",
		"CertNotAfter time.Time",
	}
	if !slices.Equal(campos, quer) {
		t.Errorf("campos de Result = %v, queria %v", campos, quer)
	}
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

// Um status ABAIXO do esperado também é falha: 200 não satisfaz quem espera
// 204. Os outros testes só usam status acima do esperado (500 contra 200).
func TestProbeStatusBelowTheExpectedIsDown(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	s := especificacao(srv.URL)
	s.ExpectedStatus = http.StatusNoContent
	res := Probe(t.Context(), client, s)
	if res.Status != StatusDown || res.ErrorKind != KindHTTPStatus || res.StatusCode != 200 {
		t.Errorf("res = %+v, queria down/http_status/200", res)
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
	if res := Probe(t.Context(), client, s); res.Status != StatusDegraded || res.ErrorKind != "" || res.StatusCode != 200 {
		t.Errorf("com limiar de 50ms: %+v, queria degraded sem erro e com status 200", res)
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

// A falha sem resposta também grava a latência, e não zero. A folga de meio
// prazo existe porque o prazo começa a contar antes de a medição começar.
func TestProbeFailureWithoutResponseRecordsTheLatency(t *testing.T) {
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
	if res := Probe(t.Context(), client, s); res.Latency < s.Timeout/2 {
		t.Errorf("latência = %s, queria ao menos metade do prazo de %s", res.Latency, s.Timeout)
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
	// O cabeçalho chegou: o status lido vai junto com a falha do corpo.
	if res := Probe(t.Context(), client, s); res.Status != StatusDown || res.ErrorKind != KindTimeout || res.StatusCode != 200 {
		t.Errorf("res = %+v, queria down/timeout/200", res)
	}
}

// D1c do STRIDE: com regra, o corpo é lido por lerCorpo, e não pelo
// io.Copy de cima. O pedaço que chegou antes de o prazo vencer já tem o valor
// procurado: se o erro da leitura fosse ignorado, a sonda daria up, e se o
// corpo truncado fosse julgado, content_mismatch no not_contains.
func TestProbeTimeoutWhileReadingBodyWithRule(t *testing.T) {
	for _, modo := range []targets.ContentMode{targets.ContentContains, targets.ContentNotContains} {
		t.Run(string(modo), func(t *testing.T) {
			parar := make(chan struct{})
			srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("tudo ok"))
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-parar:
				}
			})
			t.Cleanup(func() { close(parar) })

			s := comRegra(srv.URL, modo, "ok")
			s.Timeout = 200 * time.Millisecond
			if res := Probe(t.Context(), client, s); res.Status != StatusDown || res.ErrorKind != KindTimeout || res.StatusCode != 200 {
				t.Errorf("res = %+v, queria down/timeout/200", res)
			}
		})
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

// Toda sonda pede gzip ela mesma, com ou sem regra e em qualquer salto: o
// transporte do safedial não pede mais (DisableCompression), e sem o header a
// sonda sem regra baixaria seus 64 KiB crus, com mais banda e mais latência
// medida do que antes.
func TestEveryProbeAsksForGzip(t *testing.T) {
	casos := []struct {
		nome     string
		regra    bool
		redirect string // "", "mesmo host" ou "outro host"
	}{
		{"sem regra", false, ""},
		{"com regra", true, ""},
		{"sem regra, redirect no mesmo host", false, "mesmo host"},
		{"sem regra, redirect para outro host", false, "outro host"},
		{"com regra, redirect para outro host", true, "outro host"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			recebido := make(chan []string, 1)
			destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/inicio" {
					http.Redirect(w, r, "/fim", http.StatusFound)
					return
				}
				recebido <- r.Header.Values("Accept-Encoding")
			}))
			t.Cleanup(destino.Close)
			url := destino.URL
			permitidos := []netip.AddrPort{enderecoDe(t, destino)}
			switch c.redirect {
			case "mesmo host":
				url += "/inicio"
			case "outro host":
				origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, destino.URL+"/fim", http.StatusFound)
				}))
				t.Cleanup(origem.Close)
				url = origem.URL
				permitidos = append(permitidos, enderecoDe(t, origem))
			}
			s := especificacao(url)
			if c.regra {
				s = comRegra(url, targets.ContentNotContains, "erro")
			}
			res := Probe(t.Context(), safedial.NewClientAllowing(0, permitidos...), s)
			if res.Status != StatusUp {
				t.Fatalf("res = %+v, queria up", res)
			}
			if got := <-recebido; !slices.Equal(got, []string{"gzip"}) {
				t.Errorf("Accept-Encoding recebido = %q, queria [gzip]", got)
			}
		})
	}
}

// Sem regra, o Accept-Encoding do dono vale como antes: a sonda só drena o
// corpo, e trocá-lo mudaria o que o dono escolheu testar.
func TestOwnersAcceptEncodingWithoutRuleIsKept(t *testing.T) {
	recebido := make(chan []string, 1)
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		recebido <- r.Header.Values("Accept-Encoding")
	})
	s := especificacao(srv.URL)
	s.Headers = map[string]string{"Accept-Encoding": "br"}
	Probe(t.Context(), client, s)
	if got := <-recebido; !slices.Equal(got, []string{"br"}) {
		t.Errorf("Accept-Encoding recebido = %q, queria [br]", got)
	}
}

// Com regra, o descompressor é o nosso e só lê gzip: o Accept-Encoding do
// dono é ignorado e o servidor recebe só o da sonda. O cadastro já o recusa;
// isto é a defesa para um alvo que chegue aqui sem passar por ele.
func TestOwnersAcceptEncodingWithRuleIsIgnored(t *testing.T) {
	recebido := make(chan []string, 1)
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		recebido <- r.Header.Values("Accept-Encoding")
	})
	s := comRegra(srv.URL, targets.ContentNotContains, "erro")
	s.Headers = map[string]string{"accept-encoding": "br"}
	Probe(t.Context(), client, s)
	if got := <-recebido; !slices.Equal(got, []string{"gzip"}) {
		t.Errorf("Accept-Encoding recebido = %q, queria [gzip]", got)
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

func comRegra(url string, modo targets.ContentMode, valor string) Spec {
	s := especificacao(url)
	s.Content = &RegraDaSonda{Mode: modo, Value: []byte(valor)}
	return s
}

// Spec §5.3: a regra julga até 64 KiB. Um corpo de exatamente 64 KiB é lido
// inteiro; com um byte a mais, o valor que termina nesse byte fica de fora, e
// o resultado é inconclusivo (body_limit), nunca up.
func TestProbeContentAtTheExactLimit(t *testing.T) {
	const fim = "FIM-OK"
	casos := []struct {
		nome    string
		tamanho int
		quer    Status
		tipo    ErrorKind
	}{
		{"65536 bytes", 65536, StatusUp, ""},
		{"65537 bytes", 65537, StatusDown, KindBodyLimit},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			corpo := strings.Repeat("a", c.tamanho-len(fim)) + fim
			srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(corpo))
			})
			res := Probe(t.Context(), client, comRegra(srv.URL, targets.ContentContains, fim))
			if res.Status != c.quer || res.ErrorKind != c.tipo || res.StatusCode != http.StatusOK {
				t.Errorf("res = %+v, queria %s/%q/200", res, c.quer, c.tipo)
			}
		})
	}
}

// O status esperado vem antes da regra: um 500 continua http_status, mesmo
// com um corpo que reprovaria a regra.
func TestProbeContentIsCheckedAfterTheStatus(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("erro interno"))
	})
	res := Probe(t.Context(), client, comRegra(srv.URL, targets.ContentContains, "ok"))
	if res.Status != StatusDown || res.ErrorKind != KindHTTPStatus || res.StatusCode != 500 {
		t.Errorf("res = %+v, queria down/http_status/500", res)
	}
}

func TestProbeEmptyBodyFailsContains(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s := comRegra(srv.URL, targets.ContentContains, "ok")
	s.ExpectedStatus = http.StatusNoContent
	if res := Probe(t.Context(), client, s); res.Status != StatusDown || res.ErrorKind != KindContentMismatch {
		t.Errorf("res = %+v, queria down/content_mismatch", res)
	}
}

// Sem regra, nada muda: corpo grande segue não sendo falha (D2).
func TestProbeWithoutRuleIgnoresTheBody(t *testing.T) {
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 1<<20))
	})
	if res := Probe(t.Context(), client, especificacao(srv.URL)); res.Status != StatusUp || res.ErrorKind != "" {
		t.Errorf("res = %+v, queria up sem erro", res)
	}
}

// htmlGrande é uma página de ~286 KiB que comprime pela metade: linhas de 64
// dígitos hexadecimais sorteados com semente fixa. Cada dígito carrega 4 bits
// de entropia num byte, então nenhum gzip a comprime abaixo de metade; 64 KiB
// de HTML pedem pelo menos 32 KiB comprimidos (a página inteira passa de
// 140 KiB comprimida).
var htmlGrande = sync.OnceValue(func() []byte {
	sorteio := rand.New(rand.NewPCG(1, 2))
	var b bytes.Buffer
	b.WriteString("<!doctype html><html><body><pre>\n")
	for b.Len() < 286<<10 {
		fmt.Fprintf(&b, "%016x%016x%016x%016x\n", sorteio.Uint64(), sorteio.Uint64(), sorteio.Uint64(), sorteio.Uint64())
	}
	b.WriteString("</pre></body></html>\n")
	return b.Bytes()
})

// transporteContado conta os bytes que a sonda consome do corpo da resposta,
// ainda comprimido, porque o transporte não descomprime (DisableCompression).
type transporteContado struct {
	base  http.RoundTripper
	corpo *contador
}

func (tc *transporteContado) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := tc.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	tc.corpo.fonte = resp.Body
	resp.Body = struct {
		io.Reader
		io.Closer
	}{tc.corpo, resp.Body}
	return resp, nil
}

// Sem regra, o teto de 64 KiB vale sobre o corpo descomprimido, como antes do
// PR 5, quando o transporte do Go descomprimia: numa página grande, a sonda
// lê da rede só o gzip desses 64 KiB, e não 64 KiB de gzip (3× o tráfego numa
// home de 300 KiB). O teto do teste: 64 KiB de hexadecimal pedem 32 KiB
// comprimidos, mais os 4 KiB do bufio do gzip.Reader; 40 KiB dá folga e fica
// bem longe dos 64 KiB de quem drena o gzip cru.
func TestProbeWithoutRuleDrainsTheDecompressedLimit(t *testing.T) {
	comprimido := gzipar(t, htmlGrande())
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(comprimido)
	})
	corpo := &contador{}
	contado := *client
	contado.Transport = &transporteContado{base: client.Transport, corpo: corpo}
	if res := Probe(t.Context(), &contado, especificacao(srv.URL)); res.Status != StatusUp || res.ErrorKind != "" {
		t.Fatalf("res = %+v, queria up sem erro", res)
	}
	const piso, teto = 31 << 10, 40 << 10
	if corpo.lidos < piso || corpo.lidos > teto {
		t.Errorf("%d bytes comprimidos lidos de %d, queria entre %d e %d", corpo.lidos, len(comprimido), piso, teto)
	}
}

// Sem regra, um gzip que não abre dá o mesmo resultado de antes do PR 5, quando
// o erro vinha do descompressor do transporte: down/unknown. Corpo vazio com
// Content-Encoding: gzip não tem o que descomprimir e segue up.
func TestProbeWithoutRuleAndABrokenGzipIsUnknownAsBefore(t *testing.T) {
	casos := []struct {
		nome  string
		corpo []byte
		quer  Status
		tipo  ErrorKind
	}{
		{"cabeçalho que não é gzip", []byte("isto não é gzip"), StatusDown, KindUnknown},
		{"gzip cortado no meio", gzipar(t, htmlGrande())[:8<<10], StatusDown, KindUnknown},
		{"corpo vazio", nil, StatusUp, ""},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				_, _ = w.Write(c.corpo)
			})
			res := Probe(t.Context(), client, especificacao(srv.URL))
			if res.Status != c.quer || res.ErrorKind != c.tipo || res.StatusCode != http.StatusOK {
				t.Errorf("res = %+v, queria %s/%q/200", res, c.quer, c.tipo)
			}
		})
	}
}

// 100 MiB de zeros comprimidos cabem em ~100 KiB.
var bombaGzip = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	bloco := make([]byte, 1<<20)
	for range 100 {
		if _, err := zw.Write(bloco); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
})

// D1: o teto vale sobre o corpo DESCOMPRIMIDO. A sonda pede gzip e o
// descomprime ela mesma; sem o teto, inflaria 100 MiB. Este teste olha o
// resultado de ponta a ponta; o que prova que a bomba não é inflada inteira é
// TestLerCorpoDaRegraCapsTheDecompressedBody, que conta os bytes consumidos.
func TestGzipBombStopsAtLimit(t *testing.T) {
	bomba := bombaGzip()
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(bomba)
	})
	s := comRegra(srv.URL, targets.ContentContains, "x")
	s.Timeout = 10 * time.Second
	inicio := time.Now()
	res := Probe(t.Context(), client, s)
	if gasto := time.Since(inicio); gasto >= 2*time.Second {
		t.Errorf("a sonda levou %v, queria menos de 2 s", gasto)
	}
	if res.Status != StatusDown || res.ErrorKind != KindBodyLimit || res.StatusCode != http.StatusOK {
		t.Errorf("res = %+v, queria down/body_limit/200", res)
	}
}

// gzipar comprime b uma vez.
func gzipar(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// T5 do STRIDE: com regra, a sonda só lê um corpo sem codificação ou com
// exatamente um gzip, somando todas as linhas de Content-Encoding (RFC 9110
// §5.3: duas linhas valem "gzip, gzip"). O resto não tem o que a regra julgue:
// content_encoding, e não body_limit, que diria ao dono "resposta grande
// demais". Julgar bytes ainda comprimidos daria up falso no not_contains, e o
// corpo de erro vai comprimido duas vezes justamente para que uma camada só
// desfeita ainda esconda a palavra.
func TestUnsolicitedContentEncodingIsInconclusive(t *testing.T) {
	certo := []byte("tudo certo")
	comErro := []byte("deu erro aqui")
	casos := []struct {
		nome      string
		encodings []string
		corpo     []byte
		quer      Status
		tipo      ErrorKind
	}{
		{"br", []string{"br"}, certo, StatusDown, KindContentEncoding},
		{"zstd", []string{"zstd"}, certo, StatusDown, KindContentEncoding},
		{"deflate", []string{"deflate"}, certo, StatusDown, KindContentEncoding},
		{"gzip duas vezes", []string{"gzip, gzip"}, gzipar(t, gzipar(t, comErro)), StatusDown, KindContentEncoding},
		{"gzip em duas linhas", []string{"gzip", "gzip"}, gzipar(t, gzipar(t, comErro)), StatusDown, KindContentEncoding},
		{"gzip e br em duas linhas", []string{"gzip", "br"}, gzipar(t, gzipar(t, comErro)), StatusDown, KindContentEncoding},
		{"gzip único passa", []string{"gzip"}, gzipar(t, certo), StatusUp, ""},
		{"gzip único é julgado", []string{"gzip"}, gzipar(t, comErro), StatusDown, KindContentMismatch},
		{"GZIP em outra caixa", []string{"GZIP"}, gzipar(t, certo), StatusUp, ""},
		{"gzip com identity", []string{"identity", "gzip"}, gzipar(t, certo), StatusUp, ""},
		{"identity", []string{"identity"}, certo, StatusUp, ""},
		{"gzip que não é gzip", []string{"gzip"}, certo, StatusDown, KindContentEncoding},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
				for _, e := range c.encodings {
					w.Header().Add("Content-Encoding", e)
				}
				_, _ = w.Write(c.corpo)
			})
			res := Probe(t.Context(), client, comRegra(srv.URL, targets.ContentNotContains, "erro"))
			if res.Status != c.quer || res.ErrorKind != c.tipo || res.StatusCode != http.StatusOK {
				t.Errorf("res = %+v, queria %s/%q/200", res, c.quer, c.tipo)
			}
		})
	}
}

// O mesmo depois de um redirect para outro host. Nesse salto o safedial
// limpa os headers do alvo, inclusive o Accept-Encoding da sonda; se o
// transporte pedisse gzip por conta própria, ele desfaria uma camada e
// apagaria as linhas de Content-Encoding antes de a sonda vê-las.
func TestContentEncodingAfterARedirectToAnotherHost(t *testing.T) {
	duasVezes := gzipar(t, gzipar(t, []byte("deu erro aqui")))
	destino := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Encoding", "gzip")
		w.Header().Add("Content-Encoding", "gzip")
		_, _ = w.Write(duasVezes)
	}))
	t.Cleanup(destino.Close)
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destino.URL, http.StatusFound)
	}))
	t.Cleanup(origem.Close)
	client := safedial.NewClientAllowing(0, enderecoDe(t, origem), enderecoDe(t, destino))

	res := Probe(t.Context(), client, comRegra(origem.URL, targets.ContentNotContains, "erro"))
	if res.Status != StatusDown || res.ErrorKind != KindContentEncoding || res.StatusCode != http.StatusOK {
		t.Errorf("res = %+v, queria down/content_encoding/200", res)
	}
}

// Corpo vazio não tem o que estar comprimido: com qualquer Content-Encoding,
// a regra julga o vazio, como julgaria sem o header.
func TestEmptyBodyWithContentEncodingIsJudged(t *testing.T) {
	casos := []struct {
		nome string
		modo targets.ContentMode
		quer Status
		tipo ErrorKind
	}{
		{"not_contains passa", targets.ContentNotContains, StatusUp, ""},
		{"contains reprova", targets.ContentContains, StatusDown, KindContentMismatch},
	}
	for _, c := range casos {
		for _, encoding := range []string{"br", "gzip"} {
			t.Run(c.nome+" com "+encoding, func(t *testing.T) {
				srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Encoding", encoding)
				})
				res := Probe(t.Context(), client, comRegra(srv.URL, c.modo, "erro"))
				if res.Status != c.quer || res.ErrorKind != c.tipo || res.StatusCode != http.StatusOK {
					t.Errorf("res = %+v, queria %s/%q/200", res, c.quer, c.tipo)
				}
			})
		}
	}
}

// I2: a regra não troca o cliente. O salto para outra porta do 127.0.0.1, não
// liberada, continua bloqueado pelo safedial.
func TestContentRuleRedirectToInternalIsBlocked(t *testing.T) {
	interno := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(interno.Close)
	srv, client := servidor(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, interno.URL, http.StatusFound)
	})
	res := Probe(t.Context(), client, comRegra(srv.URL, targets.ContentContains, "ok"))
	if res.Status != StatusDown || res.ErrorKind != KindBlocked {
		t.Errorf("res = %+v, queria down/blocked", res)
	}
}
