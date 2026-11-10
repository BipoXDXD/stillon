package safedial

import (
	"cmp"
	"context"
	"errors"
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

// atrasoDaReserva é a vantagem da família preferida sobre a outra. É o valor
// padrão do FallbackDelay do net.Dialer.
const atrasoDaReserva = 300 * time.Millisecond

// MaxProbeTimeout é o maior prazo que uma sonda pode ter (timeout_ms vai até
// 30000). Os limites de fase do transporte são iguais a ele: nunca cortam uma
// sonda antes do prazo dela e só alcançam o trabalho que ela já abandonou.
const MaxProbeTimeout = 30 * time.Second

// familiasNaCorrida são IPv4 e IPv6.
const familiasNaCorrida = 2

var errSemEndereco = errors.New("nenhum endereço para discar")

// NewTransport devolve um http.Transport que se recusa a conectar em
// endereços internos.
//
// Um nome é resolvido uma única vez, aqui, e basta um endereço interno na
// resposta para o nome inteiro ser recusado: um nome que mistura interno e
// externo é suspeito, e nenhum dos dois é contatado.
//
// Validar a URL antes de discar não bastaria: quem controla o DNS do domínio
// faz rebinding — a resolução da validação devolve um IP público e a da
// conexão devolve um privado. A janela entre as duas resoluções é o bug. Ela
// se fecha discando por IP o conjunto já validado, sem resolver de novo.
//
// O ControlContext, que o Go chama depois de criar o socket e antes de discar,
// continua validando cada endereço. É a segunda barreira para os nomes e a
// única para o IP literal.
func NewTransport() *http.Transport { return newTransport(nil) }

func newTransport(allowed []netip.AddrPort) *http.Transport {
	permitir := func(address string) error {
		if liberado(address, allowed) {
			return nil
		}
		return AllowAddr(address)
	}
	dialer := novoDialer(permitir)

	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if err := allowHostName(address); err != nil {
				return nil, err
			}
			host, port, ehNome := nomeEPorta(address)
			if !ehNome {
				return dialer.DialContext(ctx, network, address)
			}
			ips, err := resolverEValidar(ctx, host, port, permitir)
			if err != nil {
				return nil, err
			}
			return discarConjunto(ctx, ips, func(ctx context.Context, ip netip.Addr) (net.Conn, error) {
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			})
		},

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

		// O prazo da sonda é o do context da requisição (Probe faz
		// context.WithTimeout(ctx, s.Timeout)). Nenhum limite de fase pode
		// cortar antes dele: um alvo com timeout_ms=20000 que manda o
		// cabeçalho aos 12s seria "down" aos 10s, um falso negativo. Por isso
		// não há ResponseHeaderTimeout.
		//
		// Mas o net/http disca com context.WithoutCancel (Go 1.23+): o dial
		// e o handshake que a sonda abandonou não veem o prazo dela. O Probe
		// os cancela com CloseIdleConnections, efeito que a documentação não
		// promete; o teto abaixo e o Timeout do Dialer (novoDialer) são a
		// defesa se isso mudar. Iguais ao maior prazo de sonda, não cortam
		// nenhuma sonda viva. TestTransportLeavesTheDeadlineToTheContext e
		// TestTransportBoundsTheAbandonedHandshake travam o valor.
		TLSHandshakeTimeout: MaxProbeTimeout,
	}
}

// novoDialer valida cada endereço no ControlContext, que o Go chama depois de
// criar o socket e antes de discar. O Timeout só alcança o dial que a sonda já
// abandonou (ver TLSHandshakeTimeout em newTransport).
func novoDialer(permitir func(address string) error) *net.Dialer {
	return &net.Dialer{
		Timeout: MaxProbeTimeout,
		ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
			return permitir(address)
		},
	}
}

// allowHostName barra o nome de rótulo único ANTES da resolução. O
// ControlContext só vê o IP já resolvido: ele bloqueia o nome que existe na
// rede interna, mas o que não existe falharia antes, no DNS, com outro
// error_kind. Barrar aqui iguala os dois casos, inclusive num redirect.
func allowHostName(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// Endereço ilegível: o próprio dialer devolve o erro.
		return nil
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return nil
	}
	if IsSingleLabel(host) {
		return fmt.Errorf("%w: host de rótulo único %q", ErrBlocked, host)
	}
	return nil
}

// nomeEPorta separa host e porta de address e diz se o host é um nome. IP
// literal e endereço ilegível ficam com o próprio dialer.
func nomeEPorta(address string) (host, port string, ehNome bool) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", false
	}
	_, err = netip.ParseAddr(host)
	return host, port, err != nil
}

// resolverEValidar faz a única resolução do nome e recusa o conjunto inteiro
// se um endereço não puder ser alcançado. O que devolve é o que será discado.
func resolverEValidar(ctx context.Context, host, port string, permitir func(address string) error) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if err := permitir(net.JoinHostPort(ip.String(), port)); err != nil {
			return nil, fmt.Errorf("nome %q recusado por inteiro: %w", host, err)
		}
	}
	return ips, nil
}

// discarIP abre a conexão para um endereço já validado.
type discarIP func(ctx context.Context, ip netip.Addr) (net.Conn, error)

type resultadoDoDial struct {
	conn net.Conn
	err  error
}

// discarConjunto faz com o conjunto validado o que o net.Dialer faria com o
// nome (Happy Eyeballs, RFC 6555): a família do primeiro endereço larga na
// frente, e a outra entra depois de atrasoDaReserva ou assim que a primeira
// falhar. O prazo por fases não serve aqui: o http.Transport tira o deadline
// do context do dial, e uma família com pacotes descartados consumiria a sonda
// inteira, derrubando um alvo que responde na outra.
func discarConjunto(ctx context.Context, ips []netip.Addr, discar discarIP) (net.Conn, error) {
	primarios, reservas := separarPorFamilia(ips)
	if len(reservas) == 0 {
		return discarEmSerie(ctx, primarios, discar)
	}

	ctx, desistir := context.WithCancel(ctx)
	defer desistir()
	retornou := make(chan struct{})
	defer close(retornou)

	resultados := make(chan resultadoDoDial)
	correr := func(familia []netip.Addr) {
		conn, err := discarEmSerie(ctx, familia, discar)
		select {
		case resultados <- resultadoDoDial{conn: conn, err: err}:
		case <-retornou:
			if conn != nil {
				// A corrida já tem vencedor: falhar ao fechar a conexão perdedora não muda o resultado.
				_ = conn.Close()
			}
		}
	}

	go correr(primarios)
	largada := time.AfterFunc(atrasoDaReserva, func() { correr(reservas) })
	defer largada.Stop()

	var primeiroErro error
	for range familiasNaCorrida {
		r := <-resultados
		if r.err == nil {
			return r.conn, nil
		}
		primeiroErro = cmp.Or(primeiroErro, r.err)
		if largada.Stop() {
			go correr(reservas)
		}
	}
	return nil, primeiroErro
}

// separarPorFamilia mantém a ordem do resolver dentro de cada família.
func separarPorFamilia(ips []netip.Addr) (primarios, reservas []netip.Addr) {
	for _, ip := range ips {
		if ip.Is4() == ips[0].Is4() {
			primarios = append(primarios, ip)
		} else {
			reservas = append(reservas, ip)
		}
	}
	return primarios, reservas
}

// discarEmSerie tenta um endereço de cada vez e, se todos falharem, devolve o
// erro do primeiro. Sem endereço, o erro impede o par nil, nil, que o
// http.Transport não saberia tratar.
func discarEmSerie(ctx context.Context, ips []netip.Addr, discar discarIP) (net.Conn, error) {
	falha := errSemEndereco
	for i, ip := range ips {
		conn, err := discar(ctx, ip)
		if err == nil {
			return conn, nil
		}
		if i == 0 {
			falha = err
		}
	}
	return nil, falha
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
