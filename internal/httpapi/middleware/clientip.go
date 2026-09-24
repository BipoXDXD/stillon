package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Proxies diz em quem confiar e qual cabeçalho ler para achar o cliente.
//
// Header vazio mantém o comportamento original: o último salto do
// X-Forwarded-For. Preenchido (no Fly, "Fly-Client-IP"), vale esse cabeçalho,
// e só ele. O motivo é o formato do Fly: o proxy dele ANEXA ao X-Forwarded-For
// o IP do próprio app (o anycast em que o cliente bateu), e o último salto
// passa a ser o mesmo para todo mundo. Com o cabeçalho dedicado, o proxy
// escreve um valor só: o do cliente.
type Proxies struct {
	Trusted []netip.Prefix
	Header  string
}

// ClientIP devolve o endereço do cliente, considerando os cabeçalhos de proxy
// APENAS quando a requisição chega de um proxy confiável.
//
// A lista de confiáveis não é burocracia: X-Forwarded-For e Fly-Client-IP são
// cabeçalhos de REQUISIÇÃO, escolhidos por quem a envia. Acreditar neles sem
// saber de quem veio seria pior que ignorá-los. Sem a lista, a falha é
// FECHADA — todo mundo divide um balde, o que é ruim mas simétrico e
// visível. Com o cabeçalho cego a falha seria ABERTA: o atacante escolheria
// um balde novo por requisição, o limitador viraria zero, e o ip_hash da
// auditoria passaria a atribuir origem falsa a terceiros.
func ClientIP(r *http.Request, p Proxies) string {
	direto, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		direto = r.RemoteAddr
	}
	if p.Header != "" {
		return peloCabecalho(r, p, direto)
	}
	if len(p.Trusted) == 0 {
		return direto
	}

	origem, err := netip.ParseAddr(direto)
	if err != nil || !confiavel(origem, p.Trusted) {
		return direto
	}

	// Quando a origem é confiável, vale o ÚLTIMO salto, não o primeiro. Um
	// proxy que anexa o endereço observado ao fim da cadeia escreveu só o
	// último elemento; tudo antes dele veio do cliente e vale tanto quanto o
	// cliente. (No Fly o último é o IP do app: por isso lá se usa o Header.)
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

// peloCabecalho lê o cabeçalho de cliente de um valor só (Fly-Client-IP).
func peloCabecalho(r *http.Request, p Proxies, direto string) string {
	valor := strings.TrimSpace(r.Header.Get(p.Header))
	if valor == "" {
		return direto
	}
	origem, err := netip.ParseAddr(direto)
	if err != nil || !confiavel(origem, p.Trusted) {
		// O cabeçalho chegou, mas não de um proxy da lista. Ou é alguém
		// forjando, ou é TRUSTED_PROXIES errado; o aviso diz qual é o
		// endereço que a conexão tem, que é o que falta para configurar.
		avisoDeProxy.avisar(direto, p.Header)
		return direto
	}
	ip, err := netip.ParseAddr(valor)
	if err != nil || ip.Zone() != "" {
		return direto
	}
	return ip.Unmap().String()
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

// avisoDeProxy é global de propósito: o aviso é por processo, não por
// cadeia de rotas. Quatro limitadores com um aviso cada inundariam o log com
// a mesma frase.
var avisoDeProxy = &aviso{agora: time.Now, intervalo: time.Minute}

// aviso limita o log de "cabeçalho de proxy ignorado" a um por intervalo.
// Com TRUSTED_PROXIES errado, TODA requisição cairia aqui.
type aviso struct {
	mu        sync.Mutex
	ultimo    time.Time
	agora     func() time.Time
	intervalo time.Duration
}

func (a *aviso) avisar(remoto, cabecalho string) {
	a.mu.Lock()
	agora := a.agora()
	if !a.ultimo.IsZero() && agora.Sub(a.ultimo) < a.intervalo {
		a.mu.Unlock()
		return
	}
	a.ultimo = agora
	a.mu.Unlock()
	slog.Warn("cabeçalho de IP do cliente ignorado: a conexão não vem de TRUSTED_PROXIES",
		"cabecalho", cabecalho, "remote_addr", remoto)
}
