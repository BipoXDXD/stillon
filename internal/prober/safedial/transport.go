package safedial

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"syscall"
	"time"
)

// maxRedirects limita a cadeia de redirecionamentos. Três é suficiente para
// uso legítimo (http→https, com→com.br) e curto o bastante para não virar
// ferramenta de varredura.
const maxRedirects = 3

// MaxBodyBytes é quanto do corpo o sondador pode ler. O monitor não precisa
// do conteúdo; ler mais do que isso só criaria canal de exfiltração e pressão
// de memória.
const MaxBodyBytes = 64 << 10

// UserAgent é a identificação pública; um header personalizado do alvo nunca
// pode ocupar seu lugar num salto que exige a remoção de segredos.
const UserAgent = "StillOn/1.0 (+monitor de disponibilidade)"

// maxResponseHeaderBytes é o teto dos cabeçalhos de uma resposta. Acima
// dele, a requisição falha e a sonda sai down (unknown).
const maxResponseHeaderBytes = 64 << 10

// NewTransport devolve um http.Transport que se recusa a conectar em
// endereços internos.
//
// A validação acontece dentro de ControlContext, que o Go chama depois de
// criar o socket e antes de discar, recebendo o endereço já resolvido.
//
// Validar a URL antes de discar não bastaria: quem controla o DNS do domínio
// faz rebinding — a resolução da validação devolve um IP público e a da
// conexão devolve um privado. A janela entre as duas resoluções é o bug, e
// validar no ControlContext a fecha, porque ali o endereço é exatamente o que
// será conectado.
func NewTransport() *http.Transport { return newTransport(nil) }

func newTransport(allowed []netip.AddrPort) *http.Transport {
	dialer := &net.Dialer{
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			if liberado(address, allowed) {
				return nil
			}
			return AllowAddr(address)
		},
	}

	return &http.Transport{
		DialContext: dialer.DialContext,

		// Sem proxy: HTTP_PROXY no ambiente faria toda requisição passar por
		// um intermediário, e o ControlContext validaria o IP do proxy em vez
		// do IP do alvo.
		Proxy: nil,

		// Sem keep-alive: conexão reusada não passa pelo dialer e, portanto,
		// não é revalidada. Cada sonda também deve medir uma conexão nova
		// para que a latência seja comparável entre execuções.
		// TestEachRequestOpensANewConnection trava isto por comportamento.
		DisableKeepAlives: true,

		ForceAttemptHTTP2: true,

		// C1 (Plano 10): o padrão do net/http é 10 MiB de cabeçalho por
		// resposta, e o HTTP/2 herda este número. Com 64 sondas em voo, um
		// alvo hostil segurava ~660 MiB de heap. Nenhum alvo legítimo de
		// monitoramento, nem o Discord ou o Resend, manda mais que isto.
		MaxResponseHeaderBytes: maxResponseHeaderBytes,

		// Sem Timeout no Dialer, sem TLSHandshakeTimeout, sem
		// ResponseHeaderTimeout: propositalmente nenhum limite por fase. O
		// prazo é só o do context da requisição (Probe faz
		// context.WithTimeout(ctx, s.Timeout); timeout_ms vai até 30000). Um
		// limite de fase fixo cortaria ANTES do timeout_ms configurado — por
		// exemplo, um alvo com timeout_ms=20000 cujo servidor manda o
		// cabeçalho aos 12s seria marcado "down" aos 10s, um falso negativo.
		// A ausência não é testável observando o dialer: NewTransport()
		// devolve um *http.Transport comum, e o campo Timeout do
		// net.Dialer embutido no DialContext não é inspecionável de fora do
		// pacote net. TestTransportLeavesTheDeadlineToTheContext trava só os
		// dois campos do próprio http.Transport.
	}
}

// liberado diz se address está na lista de exceções exatas. O Unmap iguala
// 127.0.0.1 e ::ffff:127.0.0.1, que o sistema pode entregar em qualquer das
// duas formas.
func liberado(address string, allowed []netip.AddrPort) bool {
	if len(allowed) == 0 {
		return false
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return false
	}
	return slices.Contains(allowed, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
}

// NewClient monta o cliente completo, com limite de redirecionamentos e
// revalidação de esquema a cada salto. timeout 0 deixa o prazo por conta do
// context da requisição, que é como o sondador trabalha.
func NewClient(timeout time.Duration) *http.Client { return newClient(timeout, nil) }

// NewClientAllowing é NewClient com exceções EXATAS de IP:porta, que passam
// pelo dial mesmo estando na lista de bloqueio.
//
// Existe para teste: todo httptest.Server escuta em 127.0.0.1, que o
// safedial bloqueia, e sem esta porta nenhum teste do sondador conseguiria
// sondar coisa alguma. A exceção é por porta, e não por faixa, de propósito:
// um teste de redirecionamento precisa que OUTRA porta do mesmo 127.0.0.1
// continue bloqueada.
//
// Entra em panic com endereço que não seja loopback (D15). Nenhum código de
// produção chama esta função, e o panic garante que ela não vire, por
// engano, liberação de rede interna.
func NewClientAllowing(timeout time.Duration, allowed ...netip.AddrPort) *http.Client {
	norm := make([]netip.AddrPort, 0, len(allowed))
	for _, ap := range allowed {
		a := ap.Addr().Unmap()
		if !a.IsLoopback() {
			panic(fmt.Sprintf("safedial: NewClientAllowing só aceita loopback, recebeu %s", ap))
		}
		norm = append(norm, netip.AddrPortFrom(a, ap.Port()))
	}
	return newClient(timeout, norm)
}

func newClient(timeout time.Duration, allowed []netip.AddrPort) *http.Client {
	return &http.Client{
		Transport: newTransport(allowed),
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// net/http já inclui a requisição original em via, então a
			// comparação é estrita: com maxRedirects = 3 o cliente segue
			// três saltos e para no quarto, que é o que a constante diz.
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: mais de %d redirecionamentos", ErrRedirect, maxRedirects)
			}
			// O IP do destino já será revalidado no dial, porque não há
			// reuso de conexão. O que falta checar aqui é o esquema: um
			// redirecionamento para file:// ou gopher:// nunca chega a discar.
			// O motivo vai como texto (%v), para o erro não envolver
			// ErrInvalidURL, que o sondador lê como proteção de rede.
			if _, err := ValidateURL(req.URL.String()); err != nil {
				return fmt.Errorf("%w: %v", ErrRedirect, err)
			}
			if deveLimparHeaders(cadeia(via, req)) {
				limparHeaders(req.Header)
			}
			return nil
		},
	}
}

// Headers de alvo são segredo (spec §5.4), e o net/http só protege alguns
// deles: Authorization, Cookie e companhia caem quando o redirect troca de
// domínio, mas X-Api-Key, X-Auth-Token e qualquer outro seguem para onde o
// Location mandar, e num rebaixamento https→http para o mesmo host até o
// Authorization segue em claro. O net/http também compara só o nome, sem a
// porta: de 127.0.0.1:A para 127.0.0.1:B, até o Authorization ia junto.
//
// O CheckRedirect recebe a requisição do PRÓXIMO salto, com os headers já
// copiados, e o que ele mudar em req.Header é o que vai para a rede
// (TestProbeRedirectToAnotherHostDropsTargetHeaders, no pacote prober). Mas o net/http copia os
// headers da requisição ORIGINAL a cada salto, e não os do salto anterior:
// limpar um salto não limpa o seguinte. Por isso a decisão olha a cadeia
// inteira, e uma vez limpo, limpo até o fim.

// cadeia devolve a URL de cada salto, da original ao próximo.
func cadeia(via []*http.Request, prox *http.Request) []*url.URL {
	urls := make([]*url.URL, 0, len(via)+1)
	for _, r := range via {
		urls = append(urls, r.URL)
	}
	return append(urls, prox.URL)
}

// deveLimparHeaders decide se os headers do alvo ficam para trás no último
// salto da cadeia. Limpa se algum salto foi para host:porta diferente do
// original (canonicalizado como o transporte) ou se algum salto rebaixou
// https para http. Mantém a política de portas explícitas: http://a →
// https://a é o mesmo destino e mantém os headers, que passam a ir cifrados.
func deveLimparHeaders(saltos []*url.URL) bool {
	if len(saltos) < 2 {
		return false
	}
	origem := saltos[0]
	hostOriginal, ok := DialedHost(origem.Hostname())
	if !ok {
		return true
	}
	for i := 1; i < len(saltos); i++ {
		host, ok := DialedHost(saltos[i].Hostname())
		if !ok || host != hostOriginal || saltos[i].Port() != origem.Port() {
			return true
		}
		if saltos[i-1].Scheme == "https" && saltos[i].Scheme == "http" {
			return true
		}
	}
	return false
}

// limparHeaders remove todos os valores copiados do alvo, inclusive um
// User-Agent personalizado. Só recoloca a identificação pública do monitor.
// Vai junto o Referer: ele carrega a URL anterior, que também é do alvo.
func limparHeaders(h http.Header) {
	for nome := range h {
		delete(h, nome)
	}
	h.Set("User-Agent", UserAgent)
}
