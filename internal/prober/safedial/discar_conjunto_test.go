package safedial

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

var (
	ipv4A = netip.MustParseAddr("192.0.2.1")
	ipv4B = netip.MustParseAddr("192.0.2.2")
	ipv6A = netip.MustParseAddr("2001:db8::1")
)

// connDeTeste só registra se foi fechada; nenhum teste daqui abre socket.
type connDeTeste struct {
	net.Conn
	fechada atomic.Bool
}

func (c *connDeTeste) Close() error {
	c.fechada.Store(true)
	return nil
}

// discadorDeTeste responde por endereço e guarda a ordem das tentativas. Quem
// não tem resposta fica pendurado até o context acabar, como um destino que
// descarta pacotes.
type discadorDeTeste struct {
	mu         sync.Mutex
	tentativas []netip.Addr
	respostas  map[netip.Addr]resultadoDoDial
}

func (d *discadorDeTeste) discar(ctx context.Context, ip netip.Addr) (net.Conn, error) {
	d.mu.Lock()
	d.tentativas = append(d.tentativas, ip)
	resposta, respondeu := d.respostas[ip]
	d.mu.Unlock()
	if respondeu {
		return resposta.conn, resposta.err
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (d *discadorDeTeste) tentados() []netip.Addr {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]netip.Addr(nil), d.tentativas...)
}

func TestDiscarConjuntoSemEnderecoDevolveErro(t *testing.T) {
	conn, err := discarConjunto(t.Context(), nil, (&discadorDeTeste{}).discar)
	if conn != nil || !errors.Is(err, errSemEndereco) {
		t.Errorf("conn = %v, erro = %v, queria nil e errSemEndereco", conn, err)
	}
}

// Dentro da família a ordem do resolver vale, e um registro morto não encerra
// a tentativa. Sabotagem: devolver o erro do primeiro endereço.
func TestDiscarConjuntoTentaOProximoDaMesmaFamilia(t *testing.T) {
	boa := &connDeTeste{}
	d := &discadorDeTeste{respostas: map[netip.Addr]resultadoDoDial{
		ipv4A: {err: errors.New("recusada")},
		ipv4B: {conn: boa},
	}}

	conn, err := discarConjunto(t.Context(), []netip.Addr{ipv4A, ipv4B}, d.discar)

	if err != nil || conn != boa {
		t.Fatalf("conn = %v, erro = %v, queria a conexão do segundo endereço", conn, err)
	}
}

// Sem isto, a primeira família falhando deixaria a sonda sem resposta.
// Sabotagem: largar a reserva só pelo temporizador, ou trocar o erro devolvido.
func TestDiscarConjuntoFalhaDaPrimeiraFamiliaLargaAReservaNaHora(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boa := &connDeTeste{}
		d := &discadorDeTeste{respostas: map[netip.Addr]resultadoDoDial{
			ipv6A: {err: errors.New("sem rota")},
			ipv4A: {conn: boa},
		}}
		inicio := time.Now()

		conn, err := discarConjunto(t.Context(), []netip.Addr{ipv6A, ipv4A}, d.discar)

		if err != nil || conn != boa {
			t.Fatalf("conn = %v, erro = %v, queria a conexão da reserva", conn, err)
		}
		if decorrido := time.Since(inicio); decorrido != 0 {
			t.Errorf("a reserva largou depois de %v, queria na hora", decorrido)
		}
	})
}

// O caso que a corrida existe para resolver: uma família que descarta pacotes
// não pode consumir a sonda. Sabotagem: trocar a corrida por discarEmSerie.
func TestDiscarConjuntoFamiliaPenduradaNaoImpedeAReserva(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boa := &connDeTeste{}
		d := &discadorDeTeste{respostas: map[netip.Addr]resultadoDoDial{ipv4A: {conn: boa}}}
		inicio := time.Now()

		conn, err := discarConjunto(t.Context(), []netip.Addr{ipv6A, ipv4A}, d.discar)

		if err != nil || conn != boa {
			t.Fatalf("conn = %v, erro = %v, queria a conexão da reserva", conn, err)
		}
		if decorrido := time.Since(inicio); decorrido != atrasoDaReserva {
			t.Errorf("a reserva largou depois de %v, queria %v", decorrido, atrasoDaReserva)
		}
	})
}

// Quando a primeira família responde a tempo, a outra nem é contatada.
// Sabotagem: largar a reserva junto com a primeira.
func TestDiscarConjuntoPrimeiraFamiliaAtendeSemTocarNaReserva(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boa := &connDeTeste{}
		d := &discadorDeTeste{respostas: map[netip.Addr]resultadoDoDial{ipv6A: {conn: boa}}}

		conn, err := discarConjunto(t.Context(), []netip.Addr{ipv6A, ipv4A}, d.discar)
		if err != nil || conn != boa {
			t.Fatalf("conn = %v, erro = %v, queria a conexão da primeira família", conn, err)
		}

		time.Sleep(2 * atrasoDaReserva)
		synctest.Wait()
		if tentados := d.tentados(); len(tentados) != 1 || tentados[0] != ipv6A {
			t.Errorf("tentativas = %v, queria só %v", tentados, ipv6A)
		}
	})
}

// Sabotagem: devolver nil, nil quando as duas famílias falham.
func TestDiscarConjuntoTudoFalhaDevolveOPrimeiroErro(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		errPrimeiro := errors.New("sem rota")
		d := &discadorDeTeste{respostas: map[netip.Addr]resultadoDoDial{
			ipv6A: {err: errPrimeiro},
			ipv4A: {err: errors.New("recusada")},
		}}

		conn, err := discarConjunto(t.Context(), []netip.Addr{ipv6A, ipv4A}, d.discar)

		if conn != nil || !errors.Is(err, errPrimeiro) {
			t.Errorf("conn = %v, erro = %v, queria nil e o erro da primeira", conn, err)
		}
	})
}

// Sabotagem: discar com um context que não deriva do recebido.
func TestDiscarConjuntoCancelamentoEncerraAsDuasFamilias(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const prazo = time.Second
		ctx, cancelar := context.WithTimeout(t.Context(), prazo)
		defer cancelar()
		d := &discadorDeTeste{}

		conn, err := discarConjunto(ctx, []netip.Addr{ipv6A, ipv4A}, d.discar)

		if conn != nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("conn = %v, erro = %v, queria nil e DeadlineExceeded", conn, err)
		}
		if tentados := d.tentados(); len(tentados) != familiasNaCorrida {
			t.Errorf("tentativas = %v, queria as duas famílias", tentados)
		}
	})
}

// A conexão que chega depois de a corrida acabar não pode vazar. Sabotagem:
// tirar o Close do ramo de quem perdeu.
func TestDiscarConjuntoFechaAConexaoDeQuemPerdeu(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		vencedora, atrasada := &connDeTeste{}, &connDeTeste{}
		discar := func(ctx context.Context, ip netip.Addr) (net.Conn, error) {
			if ip == ipv4A {
				return vencedora, nil
			}
			<-ctx.Done()
			return atrasada, nil
		}

		conn, err := discarConjunto(t.Context(), []netip.Addr{ipv6A, ipv4A}, discar)
		if err != nil || conn != vencedora {
			t.Fatalf("conn = %v, erro = %v, queria a conexão da reserva", conn, err)
		}

		synctest.Wait()
		if !atrasada.fechada.Load() {
			t.Error("a conexão da família perdedora ficou aberta")
		}
		if vencedora.fechada.Load() {
			t.Error("a conexão vencedora foi fechada")
		}
	})
}

// O dial também roda num context sem cancelamento: sem Timeout no Dialer, um
// alvo que descarta o SYN prende o dial até o timeout de connect do sistema.
// O teto é o maior prazo de sonda, para não cortar antes de nenhum timeout_ms.
func TestDialerBoundsTheAbandonedDial(t *testing.T) {
	if got := novoDialer(nil).Timeout; got != MaxProbeTimeout {
		t.Errorf("Dialer.Timeout = %v, queria %v", got, MaxProbeTimeout)
	}
}
