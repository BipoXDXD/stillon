package safedial

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
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
		Timeout: 5 * time.Second,
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

		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
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
				return fmt.Errorf("%w: mais de %d redirecionamentos", ErrBlocked, maxRedirects)
			}
			// O IP do destino já será revalidado no dial, porque não há
			// reuso de conexão. O que falta checar aqui é o esquema: um
			// redirecionamento para file:// ou gopher:// nunca chega a discar.
			_, err := ValidateURL(req.URL.String())
			return err
		},
	}
}
