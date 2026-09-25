// Package safedial protege o sondador contra SSRF (spec §5.1).
//
// O StillOn aceita uma URL arbitrária do usuário e faz uma requisição a
// partir do servidor — ou seja, é um proxy por construção. Sem esta guarda,
// um alvo cadastrado como http://169.254.169.254/ transforma o monitor em
// ferramenta de varredura da rede interna e de roubo de credenciais de nuvem.
//
// A defesa central não está em validar a URL, e sim em validar o endereço IP
// no instante da conexão — ver transport.go.
package safedial

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

var (
	ErrBlocked    = errors.New("endereço bloqueado")
	ErrInvalidURL = errors.New("URL inválida")
	// ErrRedirect é o redirecionamento recusado: saltos demais ou um
	// Location que não é http(s) sem credenciais (C3 do Plano 10). Não
	// envolve ErrBlocked nem ErrInvalidURL de propósito: é configuração do
	// site, e não a proteção de rede agindo. O IP interno num salto continua
	// ErrBlocked, no dial.
	ErrRedirect = errors.New("redirecionamento recusado")
)

// blockedPrefixes cobre tudo que não deve ser alcançável a partir do
// sondador. Cada faixa tem um motivo concreto de estar aqui.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "este host"
	netip.MustParsePrefix("10.0.0.0/8"),      // RFC1918
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT
	netip.MustParsePrefix("127.0.0.0/8"),     // loopback
	netip.MustParsePrefix("169.254.0.0/16"),  // link-local, inclui metadata de nuvem
	netip.MustParsePrefix("172.16.0.0/12"),   // RFC1918
	netip.MustParsePrefix("192.0.0.0/24"),    // atribuições IETF
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("192.168.0.0/16"),  // RFC1918
	netip.MustParsePrefix("192.88.99.0/24"),  // relay anycast 6to4, obsoleto (RFC 7526)
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("224.0.0.0/4"),     // multicast
	netip.MustParsePrefix("240.0.0.0/4"),     // reservado
	// Inalcançável na prática: 240.0.0.0/4 vem antes e já contém este
	// endereço, então a mensagem de erro para o broadcast citará a /4.
	// Mantida pelo valor documental — a intenção de bloquear o broadcast
	// deve continuar explícita mesmo que outra faixa a cumpra.
	netip.MustParsePrefix("255.255.255.255/32"), // broadcast
	netip.MustParsePrefix("::/128"),             // não especificado
	netip.MustParsePrefix("::1/128"),            // loopback IPv6
	netip.MustParsePrefix("fc00::/7"),           // unique local
	netip.MustParsePrefix("fe80::/10"),          // link-local IPv6
	netip.MustParsePrefix("fec0::/10"),          // site-local IPv6, obsoleto (RFC 3879)
	netip.MustParsePrefix("ff00::/8"),           // multicast IPv6

	// IPv4-compatível (RFC 4291 §2.5.5.1, obsoleto): ::a.b.c.d. Unmap() NÃO
	// desfaz esta forma — só ::ffff:a.b.c.d —, então ::127.0.0.1 passaria.
	// Esta /96 também contém ::/128 e ::1/128 acima, que ficam pelo valor
	// documental, como o broadcast: cada um tem seu motivo explícito.
	netip.MustParsePrefix("::/96"),
	// IPv4-traduzido (RFC 2765, SIIT): ::ffff:0:a.b.c.d. Outra forma que
	// Unmap() não reconhece.
	netip.MustParsePrefix("::ffff:0:0:0/96"),
	// NAT64 (RFC 6052, RFC 8215): o tradutor da rede entrega o IPv4 embutido
	// nos 32 bits finais. 64:ff9b::7f00:1 é 127.0.0.1 visto através dele (R22).
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	// 6to4 (RFC 3056, obsoleto pela RFC 7526): 2002:AABB:CCDD::/48 embute o
	// IPv4 AA.BB.CC.DD, e um relay o alcança — inclusive se for privado (R22).
	// Bloquear a /16 inteira, em vez de decodificar o IPv4 embutido, é a
	// escolha fail-closed: nenhum alvo legítimo de hoje vive em 6to4.
	netip.MustParsePrefix("2002::/16"),
	// Teredo (RFC 4380): 2001:0000::/32 embute servidor e cliente IPv4 (R22).
	// É uma /32 só: 2001:4860::, do Google, é público e não pode cair aqui.
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"), // documentação IPv6
	netip.MustParsePrefix("100::/64"),      // descarte (RFC 6666)
}

// AllowIP decide se um endereço pode ser alcançado.
func AllowIP(ip netip.Addr) error {
	if !ip.IsValid() {
		return fmt.Errorf("%w: endereço inválido", ErrBlocked)
	}

	// A zona vem ANTES do Unmap, e a ordem importa (R23). netip.Prefix.Contains
	// devolve false para qualquer endereço com zona, então sem esta recusa
	// ::1%lo atravessaria a lista inteira. E Unmap() DESCARTA a zona de um
	// endereço mapeado: ::ffff:8.8.8.8%eth0 viraria 8.8.8.8 limpo e passaria
	// se a checagem viesse depois. Alvo legítimo de monitoramento nunca
	// carrega zona, então recusar é a resposta fail-closed.
	if ip.Zone() != "" {
		return fmt.Errorf("%w: zona IPv6 não é permitida (%s)", ErrBlocked, ip)
	}

	// Unmap é indispensável. ::ffff:127.0.0.1 é 127.0.0.1 vestido de IPv6:
	// sem desfazer o mapeamento, ele não casa com 127.0.0.0/8 e atravessa a
	// guarda inteira. É o bypass mais comum contra listas de bloqueio. As
	// outras formas de IPv4 dentro de IPv6, que Unmap não reconhece, estão
	// na lista como faixas próprias.
	ip = ip.Unmap()

	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return fmt.Errorf("%w: %s pertence a %s", ErrBlocked, ip, p)
		}
	}
	return nil
}

// AllowAddr valida um endereço no formato "IP:porta", que é o que o
// ControlContext do net.Dialer entrega.
//
// Notação alternativa de IP (octal como 0177.0.0.1, decimal como 2130706433)
// não é problema aqui: a validação acontece sobre o endereço já resolvido
// pelo sistema operacional, não sobre o texto digitado pelo usuário.
func AllowAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: endereço ilegível %q", ErrBlocked, addr)
	}
	if port == "" {
		return fmt.Errorf("%w: sem porta em %q", ErrBlocked, addr)
	}

	ip, err := netip.ParseAddr(host)
	if err != nil {
		// Chegar aqui significa que a função foi chamada fora do
		// ControlContext. Negar é a resposta segura.
		return fmt.Errorf("%w: %q não é IP literal", ErrBlocked, host)
	}
	return AllowIP(ip)
}

// ValidateURL é a primeira camada: rejeita o que nem deve virar requisição.
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("%w: esquema %q não é permitido", ErrInvalidURL, u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: sem host", ErrInvalidURL)
	}
	if u.User != nil {
		// Credencial embutida na URL acaba em log de acesso e em tela.
		return nil, fmt.Errorf("%w: credenciais na URL não são permitidas", ErrInvalidURL)
	}
	return u, nil
}
