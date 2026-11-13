package safedial_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// Os testes desta página fazem do loopback o "IP público da hospedagem": o
// servidor de teste é liberado pela porta, como qualquer alvo externo, e a
// faixa dele entra em PROBE_BLOCKED_CIDRS. Se a lista da hospedagem não
// vencer, a sonda conecta e o teste cai. O erro tem de citar a faixa da
// hospedagem, e não a 127.0.0.0/8 da lista fixa.

var (
	hospedagemV4 = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	hospedagemV6 = []netip.Prefix{netip.MustParsePrefix("::/64")}
)

// A VPS é dividida com outros projetos, e as portas
// deles no IP público não podem virar alvo.
func TestProbeToTheServersOwnPublicIPIsBlocked(t *testing.T) {
	cases := []struct {
		nome       string
		escuta     string
		host       string
		hospedagem []netip.Prefix
		faixa      string
	}{
		{nome: "v4 literal", escuta: "127.0.0.1", host: "127.0.0.1", hospedagem: hospedagemV4, faixa: "127.0.0.1/32"},
		{nome: "v6 literal num CIDR v6", escuta: "::1", host: "[::1]", hospedagem: hospedagemV6, faixa: "::/64"},
		{nome: "v4 mapeado em v6 cai no CIDR v4", escuta: "127.0.0.1", host: "[::ffff:127.0.0.1]", hospedagem: hospedagemV4, faixa: "127.0.0.1/32"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			porta, conexoes := servidorInterno(t, c.escuta)
			client := safedial.NewClientAllowingBlocking(prazoDaSonda, c.hospedagem, enderecosNaPorta(porta, c.escuta)...)

			exigirBloqueio(t, client, "http://"+c.host+":"+strconv.Itoa(porta)+"/", c.faixa)

			if n := conexoes.Load(); n != 0 {
				t.Errorf("o IP da hospedagem recebeu %d conexões, queria 0", n)
			}
		})
	}
}

// A conferência vem depois do DNS: um nome comum que resolve para o IP da
// hospedagem é recusado, inclusive quando o AAAA traz o v4 mapeado.
func TestNomeQueResolveParaOIPDaHospedagemERecusado(t *testing.T) {
	for _, registro := range []string{"127.0.0.1", "::ffff:127.0.0.1"} {
		t.Run(registro, func(t *testing.T) {
			resolverEntrega(t, registro)
			porta, conexoes := servidorInterno(t, "127.0.0.1")
			client := safedial.NewClientAllowingBlocking(prazoDaSonda, hospedagemV4, enderecosNaPorta(porta, "127.0.0.1")...)

			exigirBloqueio(t, client, urlDoNome(porta), "127.0.0.1/32")

			if n := conexoes.Load(); n != 0 {
				t.Errorf("o IP da hospedagem recebeu %d conexões, queria 0", n)
			}
		})
	}
}

// Com várias respostas, um IP da hospedagem recusa o nome inteiro: o ::1,
// liberado pela porta, também não recebe conexão. Sem isso, o Happy Eyeballs
// largaria pelo endereço aceito e esconderia a hospedagem atrás dele.
func TestNomeComUmaRespostaDaHospedagemERecusadoPorInteiro(t *testing.T) {
	for _, ordem := range [][]string{{"127.0.0.1", "::1"}, {"::1", "127.0.0.1"}} {
		t.Run(strings.Join(ordem, "+"), func(t *testing.T) {
			resolverEntrega(t, ordem...)
			porta, conexoes := doisLoopbacksContando(t)
			client := safedial.NewClientAllowingBlocking(prazoDaSonda, hospedagemV4, enderecosNaPorta(porta, "127.0.0.1", "::1")...)

			exigirBloqueio(t, client, urlDoNome(porta), "127.0.0.1/32")

			if n := conexoes(); n != 0 {
				t.Errorf("os loopbacks receberam %d conexões, queria 0", n)
			}
		})
	}
}

// O salto passa pelo mesmo dial. O redirecionador escuta no ::1, fora da
// faixa; o destino, no 127.0.0.1, dentro dela. Os dois estão liberados pela
// porta: quem recusa o salto é só a lista da hospedagem.
func TestRedirectParaOIPDaHospedagemERecusado(t *testing.T) {
	portaDestino, conexoes := servidorInterno(t, "127.0.0.1")
	destino := "http://127.0.0.1:" + strconv.Itoa(portaDestino) + "/"

	ouvinte, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("esta máquina não escuta em ::1: %v", err)
	}
	var redirecionou atomic.Int32
	redirecionador := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirecionou.Add(1)
		http.Redirect(w, r, destino, http.StatusFound)
	}))
	redirecionador.Listener = ouvinte
	redirecionador.Start()
	t.Cleanup(redirecionador.Close)

	liberados := append(enderecosNaPorta(portaDestino, "127.0.0.1"), enderecoDe(t, redirecionador))
	client := safedial.NewClientAllowingBlocking(prazoDaSonda, hospedagemV4, liberados...)

	exigirBloqueio(t, client, redirecionador.URL, "127.0.0.1/32")

	// Uma requisição do cliente e outra da sonda: com menos, o bloqueio teria
	// acontecido antes do salto.
	if n := redirecionou.Load(); n != 2 {
		t.Errorf("o redirecionador recebeu %d requisições, queria 2", n)
	}
	if n := conexoes.Load(); n != 0 {
		t.Errorf("o IP da hospedagem recebeu %d conexões, queria 0", n)
	}
}

// O host octal não chega a conectar, e quem age aqui é o resolver puro do Go
// (o da imagem de produção, CGO_ENABLED=0), não uma regra nossa: netip não lê
// octal, então 0300.0000.0002.0353 vira nome, e um nome só de dígitos e
// pontos não é nome de domínio para o Go (isDomainName). A resposta é "no
// such host" sem consulta ao DNS, e a sonda grava error_kind dns. O teste
// fixa esse comportamento da stdlib: se o Go passar a consultar ou a ler o
// octal, ele fica vermelho e a defesa precisa ser revista. Uma libc (o
// resolver padrão do macOS) lê o octal; aí o IP passa pelo dial validado como
// qualquer nome resolvido (TestNomeQueResolveParaOIPDaHospedagemERecusado).
func TestHostOctalNaoChegaAConectar(t *testing.T) {
	porta, conexoes := servidorInterno(t, "127.0.0.1")
	consultas := resolverQualquerNome(t, "127.0.0.1")
	client := safedial.NewClientAllowingBlocking(prazoDaSonda, hospedagemV4, enderecosNaPorta(porta, "127.0.0.1")...)
	alvo := fmt.Sprintf("http://0300.0000.0002.0353:%d/", porta)

	resp, err := client.Get(alvo)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET %s conectou (status %d)", alvo, resp.StatusCode)
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("erro = %v, queria o \"no such host\" do resolver do Go", err)
	}
	if got := consultas.nomes(); len(got) != 0 {
		t.Errorf("consultas ao DNS = %v, queria nenhuma", got)
	}
	if n := conexoes.Load(); n != 0 {
		t.Errorf("o servidor recebeu %d conexões, queria 0", n)
	}
}

// O host decimal (3221226219 é 192.0.2.235) não tem ponto: é rótulo único, e
// a recusa vem antes do DNS, sem conexão alguma. Quem age aqui é a regra do
// rótulo único, não a lista da hospedagem.
func TestHostDecimalERecusadoAntesDoDNS(t *testing.T) {
	porta, conexoes := servidorInterno(t, "127.0.0.1")
	consultas := resolverQualquerNome(t, "127.0.0.1")
	client := safedial.NewClientAllowingBlocking(prazoDaSonda, hospedagemV4, enderecosNaPorta(porta, "127.0.0.1")...)

	exigirBloqueio(t, client, fmt.Sprintf("http://3221226219:%d/", porta), "rótulo único")

	if got := consultas.nomes(); len(got) != 0 {
		t.Errorf("consultas ao DNS = %v, queria nenhuma", got)
	}
	if n := conexoes.Load(); n != 0 {
		t.Errorf("o servidor recebeu %d conexões, queria 0", n)
	}
}

// Sem faixas, o cliente é o de hoje: o liberado pela porta é alcançado, e a
// lista fixa continua valendo.
func TestSemFaixasDaHospedagemNadaMuda(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	resp, err := safedial.NewClientAllowingBlocking(prazoDaSonda, nil, enderecoDe(t, srv)).Get(srv.URL)
	if err != nil {
		t.Fatalf("endereço liberado foi recusado sem faixas da hospedagem: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, queria 204", resp.StatusCode)
	}

	exigirBloqueio(t, safedial.NewClientBlocking(prazoDaSonda, nil), srv.URL, "127.0.0.0/8")
}

type consultasDNS struct {
	mu    sync.Mutex
	lista []string
}

func (c *consultasDNS) nomes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lista...)
}

// resolverQualquerNome responde a qualquer nome com ip (no A ou no AAAA,
// conforme a família) e guarda os nomes consultados.
func resolverQualquerNome(t *testing.T, ip string) *consultasDNS {
	t.Helper()
	endereco := netip.MustParseAddr(ip)
	consultas := &consultasDNS{}
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			cliente, servidor := net.Pipe()
			go atenderQualquerNome(servidor, endereco, consultas)
			return cliente, nil
		},
	}
	t.Cleanup(func() { net.DefaultResolver = original })
	return consultas
}

// atenderQualquerNome lê uma consulta no enquadramento TCP do DNS e responde.
func atenderQualquerNome(conn net.Conn, endereco netip.Addr, consultas *consultasDNS) {
	defer conn.Close()
	var tamanho [2]byte
	if _, err := io.ReadFull(conn, tamanho[:]); err != nil {
		return
	}
	consulta := make([]byte, binary.BigEndian.Uint16(tamanho[:]))
	if _, err := io.ReadFull(conn, consulta); err != nil {
		return
	}
	resposta, err := respostaQualquerNome(consulta, endereco, consultas)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(resposta))), resposta...))
}

func respostaQualquerNome(consulta []byte, endereco netip.Addr, consultas *consultasDNS) ([]byte, error) {
	var leitor dnsmessage.Parser
	cabecalho, err := leitor.Start(consulta)
	if err != nil {
		return nil, err
	}
	pergunta, err := leitor.Question()
	if err != nil {
		return nil, err
	}
	consultas.mu.Lock()
	consultas.lista = append(consultas.lista, pergunta.Name.String())
	consultas.mu.Unlock()

	escritor := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: cabecalho.ID, Response: true, Authoritative: true})
	if err := escritor.StartQuestions(); err != nil {
		return nil, err
	}
	if err := escritor.Question(pergunta); err != nil {
		return nil, err
	}
	if err := escritor.StartAnswers(); err != nil {
		return nil, err
	}
	if err := escreverRegistro(&escritor, pergunta, endereco); err != nil {
		return nil, err
	}
	return escritor.Finish()
}
