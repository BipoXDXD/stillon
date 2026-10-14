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

// IsSingleLabel diz se o host é um nome de rótulo único, sem ponto (db,
// intranet, localhost). Pontos finais não contam: "db." e "db.." ainda são
// db. Nome assim só existe na rede local: num contêiner, o DNS do Docker o
// resolve para outro contêiner. Se ele chegasse ao DNS, a diferença entre
// "resolveu para IP interno" (blocked) e "não existe" (dns) diria quais
// nomes há na rede. Recebe o host sem colchetes nem porta; IP literal é
// com o chamador.
func IsSingleLabel(host string) bool {
	return !strings.Contains(strings.TrimRight(host, "."), ".")
}
