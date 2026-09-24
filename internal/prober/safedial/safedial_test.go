package safedial_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/BipoXDXD/vigilume/internal/prober/safedial"
)

func TestAllowIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
		porque  string
	}{
		// Públicos: liberados.
		{"8.8.8.8", false, "DNS público"},
		{"203.0.113.9", true, "TEST-NET-3, reservado para documentação"},
		{"1.1.1.1", false, "DNS público"},
		{"2606:4700:4700::1111", false, "IPv6 público"},

		// Loopback.
		{"127.0.0.1", true, "loopback"},
		{"127.255.255.254", true, "loopback, faixa inteira"},
		{"::1", true, "loopback IPv6"},

		// O bypass clássico: IPv4 embutido em IPv6. Sem Unmap, este endereço
		// não casa com 127.0.0.0/8 e atravessa a guarda inteira.
		{"::ffff:127.0.0.1", true, "loopback disfarçado de IPv6"},
		{"::ffff:169.254.169.254", true, "metadata da nuvem disfarçado de IPv6"},

		// Zona IPv6: netip.Prefix.Contains devolve false para qualquer
		// endereço com zona, e Unmap() a descarta quando o endereço é
		// mapeado — por isso a zona é checada antes. Sem recusa explícita,
		// estes dois atravessam as vinte faixas sem casar nenhuma, e
		// http://[::1%25lo]:8080/ alcança qualquer serviço em localhost.
		{"::1%lo", true, "loopback com zona IPv6"},
		{"fe80::1%eth0", true, "link-local com zona IPv6"},

		// Metadata de provedor de nuvem: o alvo número um de SSRF.
		{"169.254.169.254", true, "endpoint de metadata da AWS/GCP"},
		{"169.254.0.1", true, "link-local"},
		{"fe80::1", true, "link-local IPv6"},

		// Redes privadas.
		{"10.0.0.5", true, "RFC1918"},
		{"172.16.0.1", true, "RFC1918"},
		{"172.31.255.255", true, "RFC1918, borda da faixa"},
		{"192.168.1.1", true, "RFC1918"},
		{"fc00::1", true, "unique local IPv6"},
		{"fd00::1", true, "unique local IPv6"},

		// Bordas que costumam escapar.
		{"172.32.0.1", false, "fora da /12, é público"},
		{"0.0.0.0", true, "this host"},
		{"100.64.0.1", true, "CGNAT"},
		{"224.0.0.1", true, "multicast"},
		{"255.255.255.255", true, "broadcast"},

		// Uma mordida por faixa que antes não tinha nenhuma. Sem estes
		// casos, apagar a linha correspondente de blockedPrefixes não
		// quebraria teste algum — e uma lista de bloqueio sem trava contra
		// regressão é a que silenciosamente encolhe.
		{"192.0.0.1", true, "atribuições IETF, 192.0.0.0/24"},
		{"192.0.2.1", true, "TEST-NET-1"},
		{"198.18.0.1", true, "benchmarking, 198.18.0.0/15"},
		{"198.51.100.1", true, "TEST-NET-2"},
		{"::", true, "não especificado"},
		{"ff02::1", true, "multicast IPv6"},
		// 255.255.255.255 não serve de trava para 240.0.0.0/4, porque a
		// 255.255.255.255/32 também o cobre e as duas se mascaram. Este
		// endereço está só na /4.
		{"240.0.0.1", true, "reservado, 240.0.0.0/4"},

		// R23 — formas de IPv4 dentro de IPv6 que Unmap() NÃO desfaz. Ele só
		// reconhece ::ffff:a.b.c.d; estas três atravessariam a lista.
		{"::127.0.0.1", true, "IPv4-compatível (obsoleto), ::/96"},
		{"::ffff:0:127.0.0.1", true, "IPv4-traduzido (SIIT), ::ffff:0:0:0/96"},

		// R22 — faixas de transição: um tradutor ou relay no caminho entrega
		// o IPv4 embutido, inclusive o privado.
		{"64:ff9b::127.0.0.1", true, "NAT64 bem conhecido embutindo loopback"},
		{"64:ff9b:1::a00:5", true, "NAT64 de uso local (RFC 8215)"},
		{"2002:7f00:1::1", true, "6to4 embutindo 127.0.0.1"},
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", true, "Teredo, 2001::/32"},
		{"2001:db8::1", true, "documentação IPv6"},
		{"100::1", true, "descarte IPv6 (RFC 6666)"},
		{"192.88.99.1", true, "relay anycast 6to4 (obsoleto)"},

		// Travas contra bloqueio largo demais. Teredo é 2001:0000::/32, uma
		// /32 só. Quem "simplificar" para 2001::/16 derruba o Google.
		{"2001:4860:4860::8888", false, "DNS público do Google, fora do Teredo"},
		{"2003::1", false, "fora do 6to4"},

		// R23 — Unmap() DESCARTA a zona de um endereço mapeado: ::ffff:8.8.8.8%eth0
		// vira 8.8.8.8 limpo. Com a zona checada depois do Unmap, este passava,
		// contrariando a regra "alvo legítimo nunca carrega zona".
		{"::ffff:8.8.8.8%eth0", true, "IPv4 mapeado com zona"},
	}

	for _, c := range cases {
		t.Run(c.ip, func(t *testing.T) {
			addr, err := netip.ParseAddr(c.ip)
			if err != nil {
				t.Fatalf("IP de teste inválido: %v", err)
			}

			err = safedial.AllowIP(addr)
			gotBlocked := err != nil

			if gotBlocked != c.blocked {
				t.Errorf("AllowIP(%s) bloqueou=%v, queria %v (%s)",
					c.ip, gotBlocked, c.blocked, c.porque)
			}
			if gotBlocked && !errors.Is(err, safedial.ErrBlocked) {
				t.Errorf("erro = %v, queria envolver ErrBlocked", err)
			}
		})
	}
}

func TestValidateURL(t *testing.T) {
	cases := []struct {
		raw    string
		ok     bool
		porque string
	}{
		{"https://exemplo.com/health", true, "caso normal"},
		{"http://exemplo.com:8080/x", true, "porta explícita"},
		{"file:///etc/passwd", false, "esquema file lê disco do servidor"},
		{"gopher://exemplo.com/", false, "gopher permite forjar protocolo arbitrário"},
		{"dict://exemplo.com/", false, "esquema não permitido"},
		{"ftp://exemplo.com/", false, "esquema não permitido"},
		{"", false, "vazio"},
		{"https://", false, "sem host"},
		{"exemplo.com", false, "sem esquema"},
		{"https://user:senha@exemplo.com/", false, "credencial na URL vaza em log"},
	}

	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			_, err := safedial.ValidateURL(c.raw)
			if (err == nil) != c.ok {
				t.Errorf("ValidateURL(%q) erro=%v, queria ok=%v (%s)", c.raw, err, c.ok, c.porque)
			}
			// A spec exige erro verificável por errors.Is. Sem esta
			// asserção, trocar fmt.Errorf("%w: ...") por errors.New
			// deixaria o teste verde e quebraria o contrato.
			if err != nil && !errors.Is(err, safedial.ErrInvalidURL) {
				t.Errorf("ValidateURL(%q) erro = %v, queria envolver ErrInvalidURL", c.raw, err)
			}
		})
	}
}

func TestAllowAddrRejectsNonLiteralHost(t *testing.T) {
	// ControlContext sempre recebe IP literal. Qualquer outra coisa indica
	// que a função está sendo chamada fora do lugar previsto — negar é a
	// resposta segura.
	cases := []struct {
		addr   string
		porque string
	}{
		{"exemplo.com:443", "host não literal"},
		{"sem-porta", "endereço sem porta"},
		{"1.2.3.4:", "porta vazia"},
	}

	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			err := safedial.AllowAddr(c.addr)
			if err == nil {
				t.Fatalf("esperava bloqueio para %s", c.porque)
			}
			if !errors.Is(err, safedial.ErrBlocked) {
				t.Errorf("erro = %v, queria envolver ErrBlocked", err)
			}
		})
	}
}

// TestAllowIPRejectsTheZeroAddress trava a única linha descoberta que é uma
// guarda de segurança, e não um fmt.Errorf de infraestrutura.
//
// Sem ela, um netip.Addr zerado não casa com prefixo nenhum da tabela e
// AllowIP devolve nil — ou seja, PERMITE. A guarda é o que separa "endereço
// que não parseou" de "liberado". Hoje nenhum consumidor chega aqui com Addr
// inválido (o safedial ainda não tem consumidor, R5), mas o prober do Plano 2
// vai passar endereços vindos de resolução de DNS, e aí a guarda é a única
// coisa entre um erro de resolução e uma requisição para onde não devia.
//
// O caso não cabe na tabela acima porque ela parte de strings, e não existe
// string que produza um Addr inválido: o zero value só se obtém direto.
func TestAllowIPRejectsTheZeroAddress(t *testing.T) {
	var zerado netip.Addr

	err := safedial.AllowIP(zerado)
	if err == nil {
		t.Fatal("AllowIP liberou o endereço zerado")
	}
	if !errors.Is(err, safedial.ErrBlocked) {
		t.Errorf("erro = %v, queria ErrBlocked", err)
	}
}
