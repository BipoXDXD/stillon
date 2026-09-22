package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP devolve o endereço do cliente, considerando X-Forwarded-For
// APENAS quando a requisição chega de um proxy confiável.
//
// A lista de confiáveis não é burocracia: X-Forwarded-For é um cabeçalho de
// REQUISIÇÃO, escolhido por quem a envia. Acreditar nele sem saber de quem
// veio seria pior que ignorá-lo. Hoje, atrás do proxy, a falha é FECHADA —
// todo mundo divide um balde, o que é ruim mas simétrico e visível. Com XFF
// cego a falha seria ABERTA: o atacante escolheria um balde novo por
// requisição, o limitador viraria zero, e o ip_hash da auditoria passaria a
// atribuir origem falsa a terceiros.
//
// Quando a origem é confiável, vale o ÚLTIMO salto, não o primeiro. O proxy
// da plataforma ANEXA o endereço observado ao fim da cadeia, então o último
// elemento é o único que ele escreveu; tudo antes dele veio do cliente e vale
// tanto quanto o cliente.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	direto, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		direto = r.RemoteAddr
	}
	if len(trusted) == 0 {
		return direto
	}

	origem, err := netip.ParseAddr(direto)
	if err != nil || !confiavel(origem, trusted) {
		return direto
	}

	cadeia := r.Header.Get("X-Forwarded-For")
	if cadeia == "" {
		return direto
	}
	partes := strings.Split(cadeia, ",")
	ultimo := strings.TrimSpace(partes[len(partes)-1])
	if _, err := netip.ParseAddr(ultimo); err != nil {
		// Cabeçalho ilegível não pode virar chave de balde nem hash de
		// origem: cai no endereço que a conexão realmente tem.
		return direto
	}
	return ultimo
}

func confiavel(ip netip.Addr, trusted []netip.Prefix) bool {
	ip = ip.Unmap()
	for _, p := range trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
