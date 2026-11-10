package prober

import (
	"crypto/tls"
	"crypto/x509"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// hostDoCertificado devolve o host cujo certificado a sonda avalia: o host da
// URL canonicalizado como o net/http o disca, sem o ponto final, que o cliente
// TLS também tira do ServerName. IP literal não tem SNI, e o ServerName do
// handshake vem vazio: não há certificado do host a avaliar.
func hostDoCertificado(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		return "", false
	}
	return nomeComparavel(u.Hostname())
}

// validadeDoHost dá a menor validade efetiva entre os handshakes verificados
// do host cadastrado. Handshake de outro host (redirect para terceiro) não
// conta: o aviso seria sobre o certificado alheio.
func validadeDoHost(host string, estados []tls.ConnectionState) (time.Time, bool) {
	var menor time.Time
	achou := false
	for _, estado := range estados {
		if !ehDoHost(host, estado) {
			continue
		}
		validade := validadeEfetiva(estado.VerifiedChains)
		if !achou || validade.Before(menor) {
			menor = validade
			achou = true
		}
	}
	return menor, achou
}

func ehDoHost(host string, estado tls.ConnectionState) bool {
	if estado.ServerName == "" || len(estado.VerifiedChains) == 0 {
		return false
	}
	nome, ok := nomeComparavel(estado.ServerName)
	return ok && nome == strings.TrimSuffix(host, ".")
}

// validadeEfetiva é o maior, entre as cadeias verificadas, do menor NotAfter
// de cada uma: um intermediário que vence antes da folha conta, e uma cadeia
// alternativa ainda válida evita o falso aviso.
func validadeEfetiva(cadeias [][]*x509.Certificate) time.Time {
	var maior time.Time
	for _, cadeia := range cadeias {
		if len(cadeia) == 0 {
			continue
		}
		menor := cadeia[0].NotAfter
		for _, cert := range cadeia[1:] {
			if cert.NotAfter.Before(menor) {
				menor = cert.NotAfter
			}
		}
		if menor.After(maior) {
			maior = menor
		}
	}
	return maior
}

// nomeComparavel aplica DialedHost e tira o ponto final. DialedHost mantém o
// ponto porque serve às barreiras de SSRF; aqui ele só atrapalha a comparação.
func nomeComparavel(host string) (string, bool) {
	nome, ok := safedial.DialedHost(host)
	if !ok {
		return "", false
	}
	return strings.TrimSuffix(nome, "."), true
}
