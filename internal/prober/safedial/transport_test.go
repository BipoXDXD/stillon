package safedial_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

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
	// é de redirecionamento, e não de proteção de rede (C3 do Plano 10): o
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
			req.Header.Set("User-Agent", "Vigilume/teste")
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
			if req.Header.Get("User-Agent") != "Vigilume/teste" {
				t.Errorf("User-Agent = %q, queria mantido", req.Header.Get("User-Agent"))
			}
		})
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
// também se NewClientAllowing não liberasse nada. É o mesmo buraco do R15.
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

// R15, agora como comportamento. Reuso de conexão pula o dialer e, com ele,
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
	// Sem esta asserção o teste seria o do Plano 1 de novo: o bloqueio
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

// O prazo de uma sonda é só o do context (Probe faz
// context.WithTimeout(ctx, s.Timeout)); nenhum timeout de fase no Transport
// pode cortar antes disso, ou um alvo com timeout_ms alto e resposta lenta
// viraria "down" cedo demais. Ver o comentário em newTransport.
func TestTransportLeavesTheDeadlineToTheContext(t *testing.T) {
	tr := safedial.NewTransport()
	if tr.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout = %v, queria 0: o prazo fica só com o context", tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout != 0 {
		t.Errorf("TLSHandshakeTimeout = %v, queria 0: o prazo fica só com o context", tr.TLSHandshakeTimeout)
	}
}

// C1 (Plano 10): sem teto, o net/http aceita até 10 MiB de cabeçalho por
// resposta (e o HTTP/2 herda o mesmo número). 64 sondas em voo com 10 MiB
// cada davam ~660 MiB de heap. O teto vale também para o Discord e o Resend,
// que usam este transporte. O comportamento está em TestProbe... do pacote
// prober; aqui fica o número.
func TestTransportCapsTheResponseHeaders(t *testing.T) {
	if got := safedial.NewTransport().MaxResponseHeaderBytes; got != 64<<10 {
		t.Errorf("MaxResponseHeaderBytes = %d, queria %d (64 KiB)", got, 64<<10)
	}
}
