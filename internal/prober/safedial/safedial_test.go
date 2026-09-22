package safedial_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/BipoXDXD/sentinela/internal/prober/safedial"
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
		})
	}
}

func TestAllowAddrRejectsNonLiteralHost(t *testing.T) {
	// ControlContext sempre recebe IP literal. Qualquer outra coisa indica
	// que a função está sendo chamada fora do lugar previsto — negar é a
	// resposta segura.
	if err := safedial.AllowAddr("exemplo.com:443"); err == nil {
		t.Error("esperava bloqueio para host não literal")
	}
	if err := safedial.AllowAddr("sem-porta"); err == nil {
		t.Error("esperava bloqueio para endereço sem porta")
	}
}
