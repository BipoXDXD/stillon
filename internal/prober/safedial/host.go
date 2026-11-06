package safedial

import (
	"net/url"
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

// DestinoMudouNoGo127 diz se o host da URL discava outro domínio até o Go
// 1.26: a tabela do IDNA passou do Unicode 15 ao 17, e ẞ deixou de virar ss
// para virar ß (straẞe.de ia a strasse.de e agora iria a xn--strae-oqa.de).
// Uma URL gravada assim trocaria de destino sem que o dono soubesse, levando
// os headers. Comparar as duas tabelas em todos os code points dá ẞ como o
// único caractere aceito por ambas com resultado diferente.
//
// Olha o host já decodificado: url.Parse aceita %E1%BA%9E nele.
func DestinoMudouNoGo127(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.ContainsRune(u.Hostname(), 'ẞ')
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
