package safedial

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// A conferência da hospedagem sozinha, sem a lista fixa por trás: os IPs de
// documentação já estão na lista fixa, e só assim um teste com eles cai se a
// conferência sumir. Cada faixa tem o ponto de dentro e o vizinho de fora.
func TestRecusarHospedagem(t *testing.T) {
	faixas := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/25"),
		netip.MustParsePrefix("2001:db8:1::/48"),
	}
	cases := []struct {
		nome      string
		endereco  string
		recusa    bool
		faixaNoEr string
	}{
		{nome: "v4 dentro", endereco: "192.0.2.10:443", recusa: true, faixaNoEr: "192.0.2.0/25"},
		{nome: "v4 no fim da faixa", endereco: "192.0.2.127:80", recusa: true, faixaNoEr: "192.0.2.0/25"},
		{nome: "v4 logo depois da faixa", endereco: "192.0.2.128:80"},
		{nome: "v6 dentro", endereco: "[2001:db8:1::5]:443", recusa: true, faixaNoEr: "2001:db8:1::/48"},
		{nome: "v6 na faixa vizinha", endereco: "[2001:db8:2::5]:443"},
		// O mapeado é o v4 vestido de v6: sem o Unmap, ele não casa com a /25.
		{nome: "v4 mapeado em v6", endereco: "[::ffff:192.0.2.10]:443", recusa: true, faixaNoEr: "192.0.2.0/25"},
		{nome: "v4 mapeado fora da faixa", endereco: "[::ffff:192.0.2.200]:443"},
		// Zona e endereço ilegível são recusados aqui, fora de qualquer
		// faixa, sem contar com o AllowAddr que vem depois no permitir.
		{nome: "v6 com zona fora da faixa", endereco: "[2001:db8:2::5%eth0]:443", recusa: true, faixaNoEr: "zona IPv6"},
		{nome: "v6 com zona dentro da faixa", endereco: "[2001:db8:1::5%eth0]:443", recusa: true, faixaNoEr: "zona IPv6"},
		{nome: "mapeado com zona", endereco: "[::ffff:192.0.2.200%eth0]:443", recusa: true, faixaNoEr: "zona IPv6"},
		{nome: "endereço ilegível", endereco: "alvo.example:443", recusa: true, faixaNoEr: "ilegível"},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			err := recusarHospedagem(c.endereco, faixas)
			if !c.recusa {
				if err != nil {
					t.Errorf("recusarHospedagem(%s) = %v, queria nil", c.endereco, err)
				}
				return
			}
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("recusarHospedagem(%s) = %v, queria ErrBlocked", c.endereco, err)
			}
			if !strings.Contains(err.Error(), c.faixaNoEr) {
				t.Errorf("erro = %v, queria citar %s", err, c.faixaNoEr)
			}
		})
	}
}

func TestRecusarHospedagemSemFaixasNaoRecusaNada(t *testing.T) {
	for _, endereco := range []string{"192.0.2.10:443", "[2001:db8:1::5]:443", "[::ffff:192.0.2.10]:443"} {
		if err := recusarHospedagem(endereco, nil); err != nil {
			t.Errorf("recusarHospedagem(%s, nil) = %v, queria nil", endereco, err)
		}
	}
}
