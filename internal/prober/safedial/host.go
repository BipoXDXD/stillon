package safedial

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// DialedHost canonicaliza o host como net/http: ASCII não passa pelo IDNA;
// os demais nomes usam o perfil Lookup. Erro é recusa, mesmo quando o IDNA
// devolve também uma conversão parcial. Serve à comparação do PATCH e dos
// redirects, para que as duas barreiras protejam o mesmo destino.
func DialedHost(host string) (string, bool) {
	for i := range len(host) {
		if host[i] >= utf8.RuneSelf {
			ascii, err := idna.Lookup.ToASCII(host)
			if err != nil {
				return "", false
			}
			return strings.ToLower(ascii), true
		}
	}
	return strings.ToLower(host), true
}
