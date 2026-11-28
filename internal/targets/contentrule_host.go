package targets

import (
	"net/netip"
	"net/url"
	"strings"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// hostDaRegra é a chave pela qual o teto de regras de conteúdo agrupa os
// alvos. Segue o que o transporte disca, como SameOrigin: IDNA e
// minúsculas pelo safedial.DialedHost, IP na forma canônica, sem zona nem
// mapeamento IPv4-em-IPv6. A porta fica de fora de propósito: separar por
// porta multiplicaria o teto pelas portas do mesmo host.
//
// Nunca falha: URL que não se lê dá "", que não casa com a chave de nenhuma
// URL válida (toda URL gravada passou por checkURL).
func hostDaRegra(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	h := u.Hostname()
	if addr, err := netip.ParseAddr(h); err == nil {
		h = addr.WithZone("").Unmap().String()
	} else if discado, ok := safedial.DialedHost(h); ok {
		h = discado
	} else {
		h = strings.ToLower(h)
	}
	// O único TrimRight: o "。" final só vira "." no DialedHost.
	return strings.TrimRight(h, ".")
}

// ExcedeRegrasPorHost diz se alguma chave de host (hostDaRegra) das URLs
// passa de teto. É a conferência do aceite da transferência de titularidade:
// as URLs são as dos alvos com regra de quem recebe, já com as do projeto, e
// o limite exato passa, como na criação (que recusa só quando o host já está
// no teto antes do alvo novo).
func ExcedeRegrasPorHost(urls []string, teto int) bool {
	porHost := make(map[string]int, len(urls))
	for _, u := range urls {
		h := hostDaRegra(u)
		porHost[h]++
		if porHost[h] > teto {
			return true
		}
	}
	return false
}
