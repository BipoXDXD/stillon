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

// HostInstavelNoIDNA protege contra a mudança da tabela do IDNA: ẞ vira ss na
// do Unicode 15, a que o projeto usa hoje, e ß na do 17 (straẞe.de vai a
// strasse.de numa e a xn--strae-oqa.de na outra). A tabela acompanha a versão
// do Go, então uma URL gravada assim trocaria de destino numa atualização, sem
// que o dono soubesse, levando os headers. Comparar as duas tabelas em todos
// os code points dá ẞ como o único caractere aceito por ambas com resultado
// diferente.
//
// Olha o host já decodificado: url.Parse aceita %E1%BA%9E nele.
func HostInstavelNoIDNA(raw string) bool {
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
