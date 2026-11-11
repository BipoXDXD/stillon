package safedial_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// A produção verifica o TLS com as raízes do sistema: nenhum TLSClientConfig
// próprio, logo nem raiz de teste nem InsecureSkipVerify. A CA de teste entra
// só no transporte montado pelo próprio teste. Sabotagem: TLSClientConfig com
// InsecureSkipVerify em newTransport reprova este teste.
//
// Ele olha o transporte antes do primeiro RoundTrip, e não pode ser movido
// para depois de uma requisição: no primeiro uso, o ForceAttemptHTTP2 cria um
// TLSClientConfig vazio, e o nil deixaria de valer.
func TestProductionClientKeepsDefaultVerification(t *testing.T) {
	client := safedial.NewClient(0)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transporte de produção = %T, queria *http.Transport", client.Transport)
	}
	if transport.TLSClientConfig != nil {
		t.Errorf("TLSClientConfig = %+v, queria nil (verificação padrão)", transport.TLSClientConfig)
	}
}

// A comparação acompanha o host que net/http disca, não o case folding do
// Unicode cru. Sabotagem: substituir a canonicalização por strings.EqualFold.
func TestCheckRedirectComparesCanonicalIDNAHosts(t *testing.T) {
	client := safedial.NewClient(time.Second)
	for _, caso := range []struct {
		nome, origem, destino string
		limpar                bool
	}{
		{"sharp s muda o domínio", "https://straße.de/", "https://STRAẞE.de/", true},
		{"Unicode e punycode do mesmo domínio", "https://straße.de/", "https://xn--strae-oqa.de/", false},
		{"caixa do mesmo domínio IDN", "https://BÜCHER.example/", "https://bücher.example/", false},
		{"host Unicode inválido", "https://exemplo.com/", "https://\u200d.example/", true},
		{"dois hosts inválidos não são o mesmo destino", "https://\u200d.example/", "https://\u200d.example/x", true},
	} {
		t.Run(caso.nome, func(t *testing.T) {
			origem, err := url.Parse(caso.origem)
			if err != nil {
				t.Fatal(err)
			}
			destino, err := http.NewRequest(http.MethodGet, caso.destino, nil)
			if err != nil {
				t.Fatal(err)
			}
			destino.Header.Set("X-Token", "sentinela-publica")
			if err := client.CheckRedirect(destino, []*http.Request{{URL: origem}}); err != nil {
				t.Fatal(err)
			}
			if removido := destino.Header.Get("X-Token") == ""; removido != caso.limpar {
				t.Errorf("header removido = %v, queria %v", removido, caso.limpar)
			}
		})
	}
}

// Este é o teste que importa. httptest.NewServer escuta em 127.0.0.1, então
// um cliente comum o alcança sem esforço. Se o cliente do safedial conseguir
// falar com ele, a guarda inteira é decorativa.
func TestClientCannotReachLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := safedial.NewClient(5 * time.Second)

	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("o cliente alcançou %s; a guarda não funcionou", srv.URL)
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria envolver ErrBlocked", err)
	}
}

// Nenhum teste com httptest chega a seguir um redirecionamento, porque o
// bloqueio acontece já no primeiro dial (ver o comentário acima). Este teste
// exercita CheckRedirect diretamente, que é onde o esquema é revalidado e a
// cadeia é limitada — é a cobertura real dessas duas propriedades.
func TestCheckRedirectRevalidaEsquemaELimitaSaltos(t *testing.T) {
	client := safedial.NewClient(5 * time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect é nil: nenhum salto seria revalidado")
	}

	req := func(raw string) *http.Request {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatalf("requisição de teste inválida: %v", err)
		}
		return r
	}

	// Um salto legítimo passa.
	if err := client.CheckRedirect(req("https://exemplo.com/x"), nil); err != nil {
		t.Errorf("salto legítimo recusado: %v", err)
	}

	// Um salto para esquema não permitido é recusado antes de discar. O erro
	// é de redirecionamento, e não de proteção de rede: o
	// dono que lê "destino bloqueado" procura um SSRF que não existe.
	recusado := func(err error, oque string) {
		t.Helper()
		if !errors.Is(err, safedial.ErrRedirect) || errors.Is(err, safedial.ErrBlocked) || errors.Is(err, safedial.ErrInvalidURL) {
			t.Errorf("%s deu erro = %v, queria só ErrRedirect (nem ErrBlocked nem ErrInvalidURL)", oque, err)
		}
	}
	recusado(client.CheckRedirect(req("file:///etc/passwd"), nil), "salto para file://")
	recusado(client.CheckRedirect(req("ftp://exemplo.com/"), nil), "salto para ftp://")

	// A cadeia é limitada. net/http já inclui a requisição original em via,
	// então via com 3 elementos é o terceiro salto — ainda permitido — e via
	// com 4 é o quarto, que para. Isso faz maxRedirects significar o número
	// de saltos seguidos, como o nome promete.
	via := []*http.Request{
		req("https://exemplo.com/0"),
		req("https://exemplo.com/1"),
		req("https://exemplo.com/2"),
	}
	if err := client.CheckRedirect(req("https://exemplo.com/3"), via); err != nil {
		t.Errorf("terceiro salto recusado: %v", err)
	}

	via = append(via, req("https://exemplo.com/3"))
	recusado(client.CheckRedirect(req("https://exemplo.com/4"), via), "cadeia longa")
}

// A decisão de levar ou não os headers do alvo para o próximo salto, caso a
// caso, pelo próprio CheckRedirect. O rebaixamento https→http no mesmo host
// não se monta com httptest (TLS e texto claro não dividem uma porta), e é
// aqui que ele fica coberto. via é a cadeia até agora, a começar pela
// original; req é o próximo salto, com os headers que o net/http copiou.
func TestCheckRedirectDropsTargetHeadersWhenTheHopLeavesTheOrigin(t *testing.T) {
	client := safedial.NewClient(0)
	cases := []struct {
		nome   string
		cadeia []string // da original ao próximo salto
		limpa  bool
	}{
		{"mesmo host, outro caminho", []string{"http://exemplo.com/", "http://exemplo.com/x"}, false},
		{"mesmo host em maiúsculas", []string{"http://exemplo.com/", "http://EXEMPLO.com/x"}, false},
		{"outro host", []string{"http://exemplo.com/", "http://outro.com/"}, true},
		{"subdomínio é outro host", []string{"http://exemplo.com/", "http://api.exemplo.com/"}, true},
		{"mesmo host, outra porta", []string{"http://exemplo.com:8080/", "http://exemplo.com:9090/"}, true},
		{"rebaixamento https→http no mesmo host", []string{"https://exemplo.com/", "http://exemplo.com/"}, true},
		{"promoção http→https no mesmo host", []string{"http://exemplo.com/", "https://exemplo.com/"}, false},
		{"rebaixamento no fim da cadeia", []string{"http://exemplo.com/", "https://exemplo.com/", "http://exemplo.com/"}, true},
		{"rebaixamento lá atrás continua valendo", []string{"https://exemplo.com/", "http://exemplo.com/", "http://exemplo.com/y"}, true},
		{"volta à origem depois de outro host", []string{"https://exemplo.com/", "https://outro.com/", "https://exemplo.com/"}, true},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			var via []*http.Request
			for _, u := range c.cadeia[:len(c.cadeia)-1] {
				r, err := http.NewRequest(http.MethodGet, u, nil)
				if err != nil {
					t.Fatal(err)
				}
				via = append(via, r)
			}
			req, err := http.NewRequest(http.MethodGet, c.cadeia[len(c.cadeia)-1], nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("User-Agent", "StillOn/teste")
			req.Header.Set("X-Api-Key", "segredo")
			req.Header.Set("Authorization", "Bearer segredo")

			if err := client.CheckRedirect(req, via); err != nil {
				t.Fatalf("CheckRedirect = %v", err)
			}
			limpou := req.Header.Get("X-Api-Key") == "" && req.Header.Get("Authorization") == ""
			manteve := req.Header.Get("X-Api-Key") == "segredo" && req.Header.Get("Authorization") == "Bearer segredo"
			switch {
			case c.limpa && !limpou:
				t.Errorf("headers = %v, queria sem os do alvo", req.Header)
			case !c.limpa && !manteve:
				t.Errorf("headers = %v, queria os do alvo intactos", req.Header)
			}
			ua := "StillOn/teste"
			if c.limpa {
				ua = "StillOn/1.0 (+monitor de disponibilidade)"
			}
			if req.Header.Get("User-Agent") != ua {
				t.Errorf("User-Agent = %q, queria %q", req.Header.Get("User-Agent"), ua)
			}
		})
	}
}

// O Accept-Encoding: gzip é da sonda, que descomprime ela mesma, e não diz
// nada do alvo: atravessa o salto que limpa os headers. Qualquer outro valor
// é do dono e cai com os demais.
func TestCheckRedirectKeepsOnlyTheProbesAcceptEncoding(t *testing.T) {
	client := safedial.NewClient(0)
	cases := []struct {
		enviado string
		quer    []string
	}{
		{"gzip", []string{"gzip"}},
		{"br", nil},
		{"gzip, br", nil},
	}
	for _, c := range cases {
		t.Run(c.enviado, func(t *testing.T) {
			origem, err := http.NewRequest(http.MethodGet, "http://exemplo.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodGet, "http://outro.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept-Encoding", c.enviado)
			if err := client.CheckRedirect(req, []*http.Request{origem}); err != nil {
				t.Fatalf("CheckRedirect = %v", err)
			}
			if got := req.Header.Values("Accept-Encoding"); !slices.Equal(got, c.quer) {
				t.Errorf("Accept-Encoding = %q, queria %q", got, c.quer)
			}
		})
	}
}

// O transporte não pede gzip por conta própria: quem pede é a sonda. Discord
// e Resend, que não pedem, recebem o corpo sem codificação.
func TestClientDoesNotAskForGzipByItself(t *testing.T) {
	recebido := make(chan []string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recebido <- r.Header.Values("Accept-Encoding")
	}))
	t.Cleanup(srv.Close)
	resp, err := safedial.NewClientAllowing(0, enderecoDe(t, srv)).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := <-recebido; len(got) != 0 {
		t.Errorf("Accept-Encoding = %q, queria nenhum", got)
	}
}

// enderecoDe devolve o IP:porta exato em que o servidor de teste escuta.
func enderecoDe(t *testing.T, srv *httptest.Server) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("endereço do servidor de teste: %v", err)
	}
	return ap
}

// Controle positivo: sem ele, todo teste abaixo que espera bloqueio passaria
// também se NewClientAllowing não liberasse nada. Seria um teste que passa sem
// testar nada.
func TestAllowedAddressIsReachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := safedial.NewClientAllowing(5*time.Second, enderecoDe(t, srv))
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("endereço liberado foi recusado: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, queria 204", resp.StatusCode)
	}
}

// Reuso de conexão pula o dialer e, com ele,
// a revalidação do IP no ControlContext: se o keep-alive voltar, uma conexão
// aberta para um IP público serviria requisições depois de o DNS apontar
// para um interno. O servidor conta quantas conexões NOVAS viu.
func TestEachRequestOpensANewConnection(t *testing.T) {
	var novas atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			novas.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	client := safedial.NewClientAllowing(5*time.Second, enderecoDe(t, srv))
	const pedidos = 3
	for range pedidos {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		// Ler o corpo até o fim é o que devolveria a conexão ao pool se o
		// keep-alive estivesse ligado. Sem isso, o teste passaria por acaso.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	if n := novas.Load(); n != pedidos {
		t.Errorf("%d conexões novas para %d requisições: houve reuso, e conexão reusada não passa pelo ControlContext", n, pedidos)
	}
}

// Um alvo liberado que redireciona para outro endereço interno. Os dois
// servidores escutam em 127.0.0.1, em portas diferentes; só o primeiro está
// liberado. O segundo salto tem de morrer no dial, e o segundo servidor não
// pode ter recebido nada.
func TestRedirectToAnotherInternalAddressIsBlockedAtTheHop(t *testing.T) {
	var atingido atomic.Int32
	interno := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atingido.Add(1)
		w.Write([]byte("segredo interno"))
	}))
	defer interno.Close()

	var redirecionou atomic.Int32
	redirecionador := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirecionou.Add(1)
		http.Redirect(w, r, interno.URL, http.StatusFound)
	}))
	defer redirecionador.Close()

	client := safedial.NewClientAllowing(5*time.Second, enderecoDe(t, redirecionador))

	resp, err := client.Get(redirecionador.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("o redirecionamento para outro endereço interno foi seguido")
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria envolver ErrBlocked", err)
	}
	// Sem esta asserção o teste não provaria o redirect: o bloqueio
	// poderia ter acontecido no salto zero, e o redirect nunca teria rodado.
	if redirecionou.Load() != 1 {
		t.Fatalf("o redirecionador recebeu %d requisições, queria 1: o bloqueio não aconteceu no salto", redirecionou.Load())
	}
	if n := atingido.Load(); n != 0 {
		t.Errorf("o servidor interno recebeu %d requisições", n)
	}
}

func TestAllowingPanicsForNonLoopback(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewClientAllowing aceitou 10.0.0.5: a porta de teste virou liberação de rede interna")
		}
	}()
	safedial.NewClientAllowing(time.Second, netip.MustParseAddrPort("10.0.0.5:80"))
}

// O Proxy continua verificado por configuração, e não por comportamento, e
// o motivo é concreto: http.ProxyFromEnvironment NUNCA manda ao proxy
// requisições para localhost ou 127.0.0.1. Um teste com HTTP_PROXY e
// httptest passaria com o Proxy ligado — seria o padrão vacuamente verde
// de novo. Fica a trava de configuração, com o motivo escrito.
func TestTransportIgnoresEnvironmentProxy(t *testing.T) {
	if safedial.NewTransport().Proxy != nil {
		t.Error("Proxy não é nil: o sondador herdaria HTTP_PROXY do ambiente, e o ControlContext validaria o IP do proxy em vez do IP do alvo")
	}
}

// O prazo de uma sonda é o do context (Probe faz context.WithTimeout(ctx,
// s.Timeout)); nenhum limite de fase no Transport pode cortar antes do maior
// timeout_ms válido, ou um alvo com timeout_ms alto e resposta lenta viraria
// "down" cedo demais. Ver o comentário em newTransport.
func TestTransportLeavesTheDeadlineToTheContext(t *testing.T) {
	tr := safedial.NewTransport()
	if tr.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, queria 0: o prazo do cabeçalho fica só com o context", tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout != 0 && tr.TLSHandshakeTimeout < safedial.MaxProbeTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, corta antes do maior prazo de sonda (%v)", tr.TLSHandshakeTimeout, safedial.MaxProbeTimeout)
	}
}

// O net/http disca com um context sem cancelamento, e o handshake que a
// sonda abandonou só para no TLSHandshakeTimeout. Zero é sem teto: um alvo
// mudo prenderia socket e goroutines para sempre se o CloseIdleConnections
// do Probe deixar de cancelar o dial (efeito não documentado do net/http).
func TestTransportBoundsTheAbandonedHandshake(t *testing.T) {
	if got := safedial.NewTransport().TLSHandshakeTimeout; got != safedial.MaxProbeTimeout {
		t.Errorf("TLSHandshakeTimeout = %v, queria %v", got, safedial.MaxProbeTimeout)
	}
}

// Sem teto, o net/http aceita até 10 MiB de cabeçalho por
// resposta (e o HTTP/2 herda o mesmo número). 64 sondas em voo com 10 MiB
// cada davam ~660 MiB de heap. O teto vale também para o Discord e o Resend,
// que usam este transporte. O comportamento está em TestProbe... do pacote
// prober; aqui fica o número.
func TestTransportCapsTheResponseHeaders(t *testing.T) {
	if got := safedial.NewTransport().MaxResponseHeaderBytes; got != 64<<10 {
		t.Errorf("MaxResponseHeaderBytes = %d, queria %d (64 KiB)", got, 64<<10)
	}
}

// Com o context já cancelado, o dial desiste antes de conectar, e nenhum caso
// depende da resposta do DNS. O nome com domínio usa .invalid (RFC 2606): o Go
// pode iniciar a consulta em segundo plano antes de ver o cancelamento, e ela
// não pode sair com um domínio real. O nome de rótulo único é recusado antes da
// resolução, e o IP literal não passa por essa regra: um IPv6 não tem ponto, e
// quem o decide é o ControlContext.
func TestDialContextAppliesTheSingleLabelRuleOnlyToNames(t *testing.T) {
	cases := []struct {
		nome, endereco string
		quer           error
	}{
		{"rótulo único", "intranet:80", safedial.ErrBlocked},
		{"rótulo único com ponto final", "intranet.:80", safedial.ErrBlocked},
		{"nome com domínio", "stillon-e2e.invalid:80", context.Canceled},
		{"IPv4 literal", "8.8.8.8:80", context.Canceled},
		{"IPv6 literal", "[2606:4700:4700::1111]:443", context.Canceled},
	}
	transport := safedial.NewClient(0).Transport.(*http.Transport)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			conn, err := transport.DialContext(ctx, "tcp", c.endereco)
			if conn != nil {
				conn.Close()
				t.Fatal("a conexão abriu com o context cancelado")
			}
			if !errors.Is(err, c.quer) {
				t.Errorf("DialContext(%s) = %v, queria %v", c.endereco, err, c.quer)
			}
		})
	}
}
