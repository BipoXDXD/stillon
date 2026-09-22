package safedial

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
func NewTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
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
		DisableKeepAlives: true,

		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}
}

// NewClient monta o cliente completo, com limite de redirecionamentos e
// revalidação de esquema a cada salto.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: NewTransport(),
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
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
