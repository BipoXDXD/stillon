package targets_test

import (
	"testing"

	"github.com/BipoXDXD/stillon/internal/targets"
)

// A chave do host da regra de conteúdo segue o que o transporte disca.
// Caixa, ponto final, porta, IDNA (inclusive o ponto ideográfico), forma do
// IPv6, IPv4 mapeado e zona caem na mesma chave; URL que não se lê dá "", que
// nunca casa com a chave de uma URL válida.
func TestRuleHostNormalizes(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://Example.COM/a", "example.com"},
		{"https://example.com./", "example.com"},
		{"https://example.com:8443/x", "example.com"},
		{"http://example.com", "example.com"},
		{" https://example.com/ ", "example.com"},
		{"https://bücher.de/", "xn--bcher-kva.de"},
		{"https://b%C3%BCcher.de/", "xn--bcher-kva.de"},
		{"https://XN--BCHER-KVA.DE./", "xn--bcher-kva.de"},
		{"https://example。com/", "example.com"},
		{"https://example.com。/", "example.com"},
		{"http://93.184.215.14/", "93.184.215.14"},
		{"http://93.184.215.14./", "93.184.215.14"},
		{"http://[2606:2800:21F:CB07::1]/", "2606:2800:21f:cb07::1"},
		{"http://[2606:2800:021f:cb07:0:0:0:1]:8080/", "2606:2800:21f:cb07::1"},
		{"http://[::ffff:93.184.215.14]/", "93.184.215.14"},
		{"http://[fe80::1%25eth0]/", "fe80::1"},
		{"::nao e url", ""},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			if got := targets.HostDaRegra(c.url); got != c.want {
				t.Errorf("HostDaRegra(%q) = %q, queria %q", c.url, got, c.want)
			}
		})
	}
}

// Host exato (não eTLD+1) e endereço exato: hosts diferentes não dividem vaga.
func TestRuleHostDistinguishesHosts(t *testing.T) {
	pares := [][2]string{
		{"https://example.com/", "https://a.example.com/"},
		{"https://example.com/", "https://example.org/"},
		{"http://93.184.215.14/", "http://93.184.215.15/"},
		{"http://[2606:2800:21f:cb07::1]/", "http://[2606:2800:21f:cb07::2]/"},
	}
	for _, p := range pares {
		t.Run(p[0]+" x "+p[1], func(t *testing.T) {
			if a, b := targets.HostDaRegra(p[0]), targets.HostDaRegra(p[1]); a == b {
				t.Errorf("HostDaRegra(%q) e HostDaRegra(%q) deram a mesma chave %q", p[0], p[1], a)
			}
		})
	}
}
