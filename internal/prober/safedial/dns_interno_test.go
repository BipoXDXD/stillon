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
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/BipoXDXD/stillon/internal/prober"
	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// nomeInterno tem domínio de propósito: allowHostName só recusa rótulo único,
// então quem decide este nome é a checagem do IP já resolvido.
const nomeInterno = "interno.exemplo.test"

const prazoDaSonda = 5 * time.Second

// O nome é comum e a URL é válida: quem decide é o IP entregue pelo DNS.
//
// Só os casos com escuta têm servidor de verdade; num endereço fora do
// loopback não há como escutar, e ali a prova é o ErrBlocked, que sai da
// validação do conjunto antes de qualquer socket.
func TestNomeComDominioQueResolveParaIPInternoERecusado(t *testing.T) {
	cases := []struct {
		nome      string
		registros []string
		ipNoErro  string
		escuta    string
	}{
		{nome: "loopback IPv4", registros: []string{"127.0.0.1"}, ipNoErro: "127.0.0.1", escuta: "127.0.0.1"},
		{nome: "loopback IPv6", registros: []string{"::1"}, ipNoErro: "::1", escuta: "::1"},
		{nome: "privado 10/8", registros: []string{"10.0.0.5"}, ipNoErro: "10.0.0.5"},
		{nome: "privado 192.168/16", registros: []string{"192.168.1.1"}, ipNoErro: "192.168.1.1"},
		{nome: "privado 172.16/12", registros: []string{"172.16.0.1"}, ipNoErro: "172.16.0.1"},
		{nome: "privado 172.16/12, fim da faixa", registros: []string{"172.31.255.255"}, ipNoErro: "172.31.255.255"},
		{nome: "link-local IPv4", registros: []string{"169.254.0.1"}, ipNoErro: "169.254.0.1"},
		{nome: "metadata de nuvem", registros: []string{"169.254.169.254"}, ipNoErro: "169.254.169.254"},
		{nome: "link-local IPv6", registros: []string{"fe80::1"}, ipNoErro: "fe80::1"},
		{nome: "unique local IPv6", registros: []string{"fd00::1"}, ipNoErro: "fd00::1"},
		// O AAAA mapeado chega ao ControlContext já na forma IPv4.
		{nome: "loopback mapeado em IPv6", registros: []string{"::ffff:127.0.0.1"}, ipNoErro: "127.0.0.1", escuta: "127.0.0.1"},
		{nome: "privado mapeado em IPv6", registros: []string{"::ffff:10.0.0.5"}, ipNoErro: "10.0.0.5"},
		{nome: "metadata mapeado em IPv6", registros: []string{"::ffff:169.254.169.254"}, ipNoErro: "169.254.169.254"},
		{nome: "vários registros, todos internos", registros: []string{"10.0.0.5", "127.0.0.1"}, escuta: "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			resolverEntrega(t, c.registros...)
			porta, conexoes := 5432, &atomic.Int32{}
			if c.escuta != "" {
				porta, conexoes = servidorInterno(t, c.escuta)
			}
			alvo := urlDoNome(porta)

			exigirBloqueio(t, safedial.NewClient(prazoDaSonda), alvo, c.ipNoErro)

			if n := conexoes.Load(); n != 0 {
				t.Errorf("o endereço interno recebeu %d conexões, queria 0", n)
			}
		})
	}
}

// O salto de um redirecionamento passa pelo mesmo dial. O redirecionador está
// liberado por IP:porta; o destino do salto, não.
func TestSaltoParaNomeQueResolveParaIPInternoERecusado(t *testing.T) {
	resolverEntrega(t, "127.0.0.1")
	porta, conexoes := servidorInterno(t, "127.0.0.1")

	var redirecionou atomic.Int32
	redirecionador := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirecionou.Add(1)
		http.Redirect(w, r, urlDoNome(porta), http.StatusFound)
	}))
	t.Cleanup(redirecionador.Close)
	client := safedial.NewClientAllowing(prazoDaSonda, enderecoDe(t, redirecionador))

	exigirBloqueio(t, client, redirecionador.URL, "127.0.0.1")

	// Uma requisição do cliente e outra da sonda. Com menos que isso, o
	// bloqueio teria acontecido antes do salto.
	if n := redirecionou.Load(); n != 2 {
		t.Errorf("o redirecionador recebeu %d requisições, queria 2", n)
	}
	if n := conexoes.Load(); n != 0 {
		t.Errorf("o endereço interno recebeu %d conexões, queria 0", n)
	}
}

// Quando o nome entrega interno e externo juntos, o nome inteiro é suspeito: a
// recusa vem antes de qualquer conexão, e nem o externo é contatado. O papel do
// externo é de um loopback liberado por IP:porta, para o teste não sair da
// máquina. Os dois loopbacks ficam escutando e contando em todos os casos, e o
// resolver ordena os registros pela RFC 6724: alternar qual deles é o externo
// cobre o interno em qualquer posição.
func TestNomeComIPInternoEExternoNuncaContataOInterno(t *testing.T) {
	cases := []struct {
		nome      string
		registros []string
		externos  []string
		ipNoErro  string
	}{
		{nome: "loopback IPv6 é o interno", registros: []string{"127.0.0.1", "::1"}, externos: []string{"127.0.0.1"}, ipNoErro: "::1"},
		{nome: "loopback IPv4 é o interno", registros: []string{"127.0.0.1", "::1"}, externos: []string{"::1"}, ipNoErro: "127.0.0.1"},
		{nome: "loopback mapeado é o interno", registros: []string{"::1", "::ffff:127.0.0.1"}, externos: []string{"::1"}, ipNoErro: "127.0.0.1"},
		{nome: "privado mapeado é o interno", registros: []string{"127.0.0.1", "::ffff:10.0.0.5"}, externos: []string{"127.0.0.1"}, ipNoErro: "10.0.0.5"},
		{nome: "privado IPv4 é o interno", registros: []string{"::1", "192.168.1.1"}, externos: []string{"::1"}, ipNoErro: "192.168.1.1"},
		{nome: "metadata de nuvem é o interno", registros: []string{"127.0.0.1", "169.254.169.254"}, externos: []string{"127.0.0.1"}, ipNoErro: "169.254.169.254"},
		{nome: "unique local IPv6 é o interno", registros: []string{"127.0.0.1", "fd00::1"}, externos: []string{"127.0.0.1"}, ipNoErro: "fd00::1"},
		{nome: "link-local IPv6 é o interno", registros: []string{"127.0.0.1", "fe80::1"}, externos: []string{"127.0.0.1"}, ipNoErro: "fe80::1"},
		{nome: "um interno entre vários externos", registros: []string{"127.0.0.1", "::1", "10.0.0.5"}, externos: []string{"127.0.0.1", "::1"}, ipNoErro: "10.0.0.5"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			porta, conexoes := doisLoopbacksContando(t)
			resolverEntrega(t, c.registros...)
			client := safedial.NewClientAllowing(prazoDaSonda, enderecosNaPorta(porta, c.externos...)...)

			exigirBloqueio(t, client, urlDoNome(porta), c.ipNoErro)

			if n := conexoes(); n != 0 {
				t.Errorf("os loopbacks receberam %d conexões, queria 0", n)
			}
		})
	}
}

// O salto de um redirecionamento obedece à mesma regra: o externo do nome
// misto está liberado, e ainda assim nada é contatado.
func TestSaltoParaNomeComIPInternoEExternoERecusado(t *testing.T) {
	porta, conexoes := doisLoopbacksContando(t)
	resolverEntrega(t, "127.0.0.1", "::1")

	var redirecionou atomic.Int32
	redirecionador := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirecionou.Add(1)
		http.Redirect(w, r, urlDoNome(porta), http.StatusFound)
	}))
	t.Cleanup(redirecionador.Close)
	liberados := append(enderecosNaPorta(porta, "127.0.0.1"), enderecoDe(t, redirecionador))
	client := safedial.NewClientAllowing(prazoDaSonda, liberados...)

	exigirBloqueio(t, client, redirecionador.URL, "::1")

	// Uma requisição do cliente e outra da sonda. Com menos que isso, o
	// bloqueio teria acontecido antes do salto.
	if n := redirecionou.Load(); n != 2 {
		t.Errorf("o redirecionador recebeu %d requisições, queria 2", n)
	}
	if n := conexoes(); n != 0 {
		t.Errorf("os loopbacks receberam %d conexões, queria 0", n)
	}
}

// Controle positivo: com todo o conjunto externo a sonda sai up, inclusive
// quando só o segundo endereço da lista está no ar.
func TestNomeSoComIPExternoEAtendido(t *testing.T) {
	cases := []struct {
		nome      string
		registros []string
		escuta    string
	}{
		{nome: "IPv4", registros: []string{"127.0.0.1"}, escuta: "127.0.0.1"},
		{nome: "IPv6", registros: []string{"::1"}, escuta: "::1"},
		{nome: "IPv4 mapeado em IPv6", registros: []string{"::ffff:127.0.0.1"}, escuta: "127.0.0.1"},
		{nome: "dois registros, só o IPv4 no ar", registros: []string{"127.0.0.1", "::1"}, escuta: "127.0.0.1"},
		{nome: "dois registros, só o IPv6 no ar", registros: []string{"127.0.0.1", "::1"}, escuta: "::1"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			ouvintes := portaLivreNosDoisLoopbacks(t)
			no, fora := ouvintes.ipv4, ouvintes.ipv6
			if c.escuta == "::1" {
				no, fora = ouvintes.ipv6, ouvintes.ipv4
			}
			fora.Close()
			conexoes := servirContando(t, no)
			resolverEntrega(t, c.registros...)
			client := safedial.NewClientAllowing(prazoDaSonda, enderecosNaPorta(ouvintes.numero, c.registros...)...)

			res := prober.Probe(t.Context(), client, prober.Spec{
				URL: urlDoNome(ouvintes.numero), Method: http.MethodGet, Timeout: prazoDaSonda, ExpectedStatus: http.StatusOK,
			})

			if res.Status != prober.StatusUp {
				t.Errorf("sonda = %+v, queria up", res)
			}
			if n := conexoes.Load(); n != 1 {
				t.Errorf("o endereço no ar recebeu %d conexões, queria 1", n)
			}
		})
	}
}

// O que é discado é o conjunto da única resolução: a segunda resposta do
// resolver nunca é usada.
func TestConexaoUsaOConjuntoDaPrimeiraResolucao(t *testing.T) {
	ouvintes := portaLivreNosDoisLoopbacks(t)
	conexoesExterno := servirContando(t, ouvintes.ipv4)
	conexoesInterno := servirContando(t, ouvintes.ipv6)
	resolverMudaNaSegundaConsulta(t, "127.0.0.1", "::1")
	client := safedial.NewClientAllowing(prazoDaSonda, enderecosNaPorta(ouvintes.numero, "127.0.0.1")...)

	resp, err := client.Get(urlDoNome(ouvintes.numero))
	if err != nil {
		t.Fatalf("a requisição falhou: o nome foi resolvido de novo? %v", err)
	}
	resp.Body.Close()

	if n := conexoesExterno.Load(); n != 1 {
		t.Errorf("o endereço validado recebeu %d conexões, queria 1", n)
	}
	if n := conexoesInterno.Load(); n != 0 {
		t.Errorf("o endereço da segunda resposta recebeu %d conexões, queria 0", n)
	}
}

// A resolução obedece ao context de quem discou. A chamada é direta no
// DialContext porque, pelo cliente, o prazo da requisição devolveria o erro
// mesmo com a resolução pendurada.
func TestPrazoDoContextCortaAResolucao(t *testing.T) {
	const prazoDoDial = 50 * time.Millisecond
	fim := make(chan struct{})
	t.Cleanup(func() { close(fim) })
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-fim:
				return nil, net.ErrClosed
			}
		},
	}
	t.Cleanup(func() { net.DefaultResolver = original })
	transporte := safedial.NewClient(0).Transport.(*http.Transport)
	ctx, cancelar := context.WithTimeout(t.Context(), prazoDoDial)
	defer cancelar()

	retorno := make(chan error, 1)
	go func() {
		conn, err := transporte.DialContext(ctx, "tcp", net.JoinHostPort(nomeInterno, "80"))
		if conn != nil {
			conn.Close()
		}
		retorno <- err
	}()

	select {
	case err := <-retorno:
		if prober.Classify(err) != prober.KindTimeout {
			t.Errorf("erro = %v, queria timeout", err)
		}
	case <-time.After(prazoDaSonda):
		t.Fatal("o DialContext ignorou o prazo do context")
	}
}

// Um registro externo fora do ar não muda a recusa do nome. Aqui o interno é o
// 127.0.0.1, e o ::1 liberado não tem servidor.
func TestExternoForaDoArNaoAbrePassagemParaOInterno(t *testing.T) {
	ouvintes := portaLivreNosDoisLoopbacks(t)
	externoFora := netip.AddrPortFrom(netip.MustParseAddr("::1"), uint16(ouvintes.numero))
	ouvintes.ipv6.Close()
	conexoesInterno := servirContando(t, ouvintes.ipv4)
	resolverEntrega(t, "127.0.0.1", "::1")

	client := safedial.NewClientAllowing(prazoDaSonda, externoFora)
	resp, err := client.Get(urlDoNome(ouvintes.numero))
	if err == nil {
		resp.Body.Close()
		t.Fatal("a requisição chegou a algum servidor; só o interno está no ar")
	}

	var erroDNS *net.DNSError
	if errors.As(err, &erroDNS) {
		t.Fatalf("o nome nem foi resolvido, e o teste não provou nada: %v", err)
	}
	if n := conexoesInterno.Load(); n != 0 {
		t.Errorf("o endereço interno recebeu %d conexões, queria 0", n)
	}
}

// exigirBloqueio confere as duas pontas da recusa: o erro do cliente e o
// resultado que a sonda grava. ipNoErro vazio dispensa a conferência do
// endereço, que prova que a guarda julgou o IP entregue por este DNS.
func exigirBloqueio(t *testing.T, client *http.Client, alvo, ipNoErro string) {
	t.Helper()
	resp, err := client.Get(alvo)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET %s conectou (status %d), queria ErrBlocked", alvo, resp.StatusCode)
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria envolver ErrBlocked", err)
	}
	if !strings.Contains(err.Error(), ipNoErro) {
		t.Errorf("erro = %v, queria citar %s", err, ipNoErro)
	}

	res := prober.Probe(t.Context(), client, prober.Spec{
		URL: alvo, Method: http.MethodGet, Timeout: prazoDaSonda, ExpectedStatus: http.StatusOK,
	})
	if res.Status != prober.StatusDown || res.ErrorKind != prober.KindBlocked {
		t.Errorf("sonda = %+v, queria down/blocked", res)
	}
}

func urlDoNome(porta int) string {
	return "http://" + net.JoinHostPort(nomeInterno, strconv.Itoa(porta)) + "/"
}

// servidorInterno sobe um servidor em ip e devolve a porta e o contador de
// conexões TCP aceitas.
func servidorInterno(t *testing.T, ip string) (int, *atomic.Int32) {
	t.Helper()
	ouvinte, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("esta máquina não escuta em %s: %v", ip, err)
	}
	return ouvinte.Addr().(*net.TCPAddr).Port, servirContando(t, ouvinte)
}

func servirContando(t *testing.T, ouvinte net.Listener) *atomic.Int32 {
	t.Helper()
	var conexoes atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "segredo interno")
	}))
	srv.Listener.Close()
	srv.Listener = ouvinte
	srv.Config.ConnState = func(_ net.Conn, estado http.ConnState) {
		if estado == http.StateNew {
			conexoes.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return &conexoes
}

// doisLoopbacksContando deixa os dois loopbacks escutando na mesma porta e
// devolve a porta e o total de conexões TCP aceitas por eles.
func doisLoopbacksContando(t *testing.T) (int, func() int32) {
	t.Helper()
	ouvintes := portaLivreNosDoisLoopbacks(t)
	ipv4, ipv6 := servirContando(t, ouvintes.ipv4), servirContando(t, ouvintes.ipv6)
	return ouvintes.numero, func() int32 { return ipv4.Load() + ipv6.Load() }
}

func enderecosNaPorta(porta int, ips ...string) []netip.AddrPort {
	enderecos := make([]netip.AddrPort, 0, len(ips))
	for _, ip := range ips {
		enderecos = append(enderecos, netip.AddrPortFrom(netip.MustParseAddr(ip), uint16(porta)))
	}
	return enderecos
}

type ouvintesNaMesmaPorta struct {
	ipv4, ipv6 net.Listener
	numero     int
}

// portaLivreNosDoisLoopbacks reserva a mesma porta em 127.0.0.1 e ::1, porque
// um nome só tem uma porta na URL. A que o sistema escolhe no IPv4 pode estar
// ocupada no IPv6; daí as tentativas.
func portaLivreNosDoisLoopbacks(t *testing.T) ouvintesNaMesmaPorta {
	t.Helper()
	const tentativas = 20
	for range tentativas {
		ipv4, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		numero := ipv4.Addr().(*net.TCPAddr).Port
		ipv6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(numero)))
		if err == nil {
			t.Cleanup(func() { ipv4.Close(); ipv6.Close() })
			return ouvintesNaMesmaPorta{ipv4: ipv4, ipv6: ipv6, numero: numero}
		}
		ipv4.Close()
		if !strings.Contains(err.Error(), "address already in use") {
			t.Skipf("esta máquina não escuta em ::1: %v", err)
		}
	}
	t.Fatalf("nenhuma porta livre nos dois loopbacks em %d tentativas", tentativas)
	return ouvintesNaMesmaPorta{}
}

// resolverEntrega faz nomeInterno resolver para registros durante o teste.
func resolverEntrega(t *testing.T, registros ...string) {
	t.Helper()
	enderecos := enderecosDe(registros)
	instalarResolver(t, func(dnsmessage.Type) []netip.Addr { return enderecos })
}

// resolverMudaNaSegundaConsulta responde primeira só à consulta inicial de cada
// tipo (A e AAAA, as duas de uma resolução) e seguinte a todas as outras.
func resolverMudaNaSegundaConsulta(t *testing.T, primeira, seguinte string) {
	t.Helper()
	var consultasA, consultasAAAA atomic.Int32
	inicial, depois := enderecosDe([]string{primeira}), enderecosDe([]string{seguinte})
	instalarResolver(t, func(tipo dnsmessage.Type) []netip.Addr {
		consultas := &consultasA
		if tipo == dnsmessage.TypeAAAA {
			consultas = &consultasAAAA
		}
		if consultas.Add(1) == 1 {
			return inicial
		}
		return depois
	})
}

func enderecosDe(registros []string) []netip.Addr {
	enderecos := make([]netip.Addr, 0, len(registros))
	for _, r := range registros {
		enderecos = append(enderecos, netip.MustParseAddr(r))
	}
	return enderecos
}

// respostaDNS dá os endereços de nomeInterno para o tipo consultado.
type respostaDNS func(dnsmessage.Type) []netip.Addr

// instalarResolver troca o net.DefaultResolver, que é por onde o transporte
// resolve: o caminho de produção fica inteiro, e o Dial em net.Pipe mantém a
// consulta dentro do processo. Por trocar um global, estes testes não usam
// t.Parallel.
func instalarResolver(t *testing.T, responder respostaDNS) {
	t.Helper()
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			cliente, servidor := net.Pipe()
			go atenderConsultaDNS(servidor, responder)
			return cliente, nil
		},
	}
	t.Cleanup(func() { net.DefaultResolver = original })
}

// atenderConsultaDNS responde uma consulta no enquadramento de stream (RFC 1035
// §4.2.2), que é o que o resolver do Go usa num Conn sem PacketConn. Um erro
// aqui só fecha a conexão: o resolver falha, e o teste vê erro de DNS em vez do
// ErrBlocked que exige.
func atenderConsultaDNS(conn net.Conn, responder respostaDNS) {
	defer conn.Close()
	var tamanho [2]byte
	if _, err := io.ReadFull(conn, tamanho[:]); err != nil {
		return
	}
	consulta := make([]byte, binary.BigEndian.Uint16(tamanho[:]))
	if _, err := io.ReadFull(conn, consulta); err != nil {
		return
	}
	resposta, err := montarRespostaDNS(consulta, responder)
	if err != nil {
		return
	}
	quadro := binary.BigEndian.AppendUint16(nil, uint16(len(resposta)))
	_, _ = conn.Write(append(quadro, resposta...))
}

func montarRespostaDNS(consulta []byte, responder respostaDNS) ([]byte, error) {
	var leitor dnsmessage.Parser
	cabecalho, err := leitor.Start(consulta)
	if err != nil {
		return nil, err
	}
	pergunta, err := leitor.Question()
	if err != nil {
		return nil, err
	}

	codigo := dnsmessage.RCodeNameError
	var enderecos []netip.Addr
	if pergunta.Name.String() == nomeInterno+"." {
		codigo = dnsmessage.RCodeSuccess
		enderecos = responder(pergunta.Type)
	}
	escritor := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: cabecalho.ID, Response: true, Authoritative: true, RCode: codigo,
	})
	if err := escritor.StartQuestions(); err != nil {
		return nil, err
	}
	if err := escritor.Question(pergunta); err != nil {
		return nil, err
	}
	if err := escritor.StartAnswers(); err != nil {
		return nil, err
	}
	for _, endereco := range enderecos {
		if err := escreverRegistro(&escritor, pergunta, endereco); err != nil {
			return nil, err
		}
	}
	return escritor.Finish()
}

func escreverRegistro(escritor *dnsmessage.Builder, pergunta dnsmessage.Question, endereco netip.Addr) error {
	const ttlSegundos = 60
	cabecalho := dnsmessage.ResourceHeader{Name: pergunta.Name, Class: dnsmessage.ClassINET, TTL: ttlSegundos}
	switch {
	case pergunta.Type == dnsmessage.TypeA && endereco.Is4():
		return escritor.AResource(cabecalho, dnsmessage.AResource{A: endereco.As4()})
	case pergunta.Type == dnsmessage.TypeAAAA && !endereco.Is4():
		return escritor.AAAAResource(cabecalho, dnsmessage.AAAAResource{AAAA: endereco.As16()})
	}
	return nil
}
