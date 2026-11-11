package targets

import (
	"net/netip"
	"net/url"
	"strings"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
)

// hostDaRegra é a chave pela qual o teto de regras de conteúdo agrupa os
// alvos (D-T1). Segue o que o transporte disca, como SameOrigin: IDNA e
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
