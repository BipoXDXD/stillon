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

const (
	// renovarVistoApos é a idade a partir da qual uma leitura igual à guardada
	// renova observed_at; antes disso, gravar a cada sonda só gastaria escrita.
	renovarVistoApos = time.Hour
	// janelaMinimaDeTroca e fatorDaJanelaDeTroca dão quanto tempo a validade
	// guardada precisa ficar sem ser vista para uma maior a substituir:
	// max(6 h, 3 x intervalo do alvo). Num balanceador com réplicas de
	// certificados diferentes, o pior continua sendo visto e fica guardado.
	janelaMinimaDeTroca  = 6 * time.Hour
	fatorDaJanelaDeTroca = 3
)

// validadeGuardada é o pior certificado visto do alvo e quando foi visto.
type validadeGuardada struct {
	NotAfter, ObservedAt time.Time
}

// substituirValidade aplica a regra do pior certificado à leitura feita em
// em. Devolve a validade a guardar e se ela precisa ser gravada; sem gravar,
// devolve a guardada inalterada. Leitura menor substitui na hora; igual só
// renova observed_at depois de renovarVistoApos; maior só substitui quando a
// guardada ficou sem ser vista por mais que a janela de troca.
func substituirValidade(guardada *validadeGuardada, lida, em time.Time, intervalo time.Duration) (validadeGuardada, bool) {
	nova := validadeGuardada{NotAfter: lida, ObservedAt: em}
	if guardada == nil {
		return nova, true
	}
	semSerVista := em.Sub(guardada.ObservedAt)
	switch {
	case lida.Before(guardada.NotAfter):
		return nova, true
	case lida.Equal(guardada.NotAfter):
		if semSerVista > renovarVistoApos {
			return nova, true
		}
	default:
		if semSerVista > janelaDeTroca(intervalo) {
			return nova, true
		}
	}
	return *guardada, false
}

func janelaDeTroca(intervalo time.Duration) time.Duration {
	return max(janelaMinimaDeTroca, fatorDaJanelaDeTroca*intervalo)
}
