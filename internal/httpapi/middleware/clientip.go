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
//
// Header só serve atrás de um proxy que SUBSTITUI esse cabeçalho, como o do
// Fly: um proxy genérico (o Caddy, por padrão) repassa o valor que o cliente
// mandou, e aí qualquer um escolhe o próprio IP. Atrás do Caddy, vazio (B4 do
// Plano 10).
//
// AvisarXFF liga o aviso de X-Forwarded-For vindo de fora de Trusted (B3 do
// Plano 10). Em produção (PUBLIC_BASE_URL https) isso quase sempre é a lista
// errada; em desenvolvimento seria ruído.
type Proxies struct {
	Trusted   []netip.Prefix
	Header    string
	AvisarXFF bool
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
	// O endereço da conexão sai na mesma forma que os dos cabeçalhos (Plano
	// 8): sem isto, o mesmo cliente IPv4 numa pilha dupla chegava como
	// "::ffff:1.2.3.4" e ganhava um ip_hash diferente do "1.2.3.4". O que não
	// é IP (listener de unix socket, pipe de teste) continua como veio: não há
	// outro valor para usar.
	if n, ok := normalizar(direto); ok {
		direto = n
	}
	if p.Header != "" {
		return peloCabecalho(r, p, direto)
	}

	// Todas as linhas, na ordem (B2 do Plano 10): um proxy que acrescenta uma
	// linha nova em vez de concatenar (o HAProxy com option forwardfor) deixa
	// a linha do cliente em primeiro lugar, e o Get só devolveria essa.
	linhas := r.Header.Values("X-Forwarded-For")
	origem, err := netip.ParseAddr(direto)
	if err != nil || !confiavel(origem, p.Trusted) {
		// O cabeçalho chegou de uma origem fora da lista (ou a lista está
		// vazia). Em produção, é a lista errada até prova em contrário, e todo
		// cliente cai num balde só; o aviso diz qual endereço a conexão tem.
		if p.AvisarXFF && len(linhas) > 0 {
			avisoDeProxy.avisar(msgForaDaLista, direto, "X-Forwarded-For")
		}
		return direto
	}

	// Quando a origem é confiável, vale o ÚLTIMO salto, não o primeiro. Um
	// proxy que anexa o endereço observado ao fim da cadeia escreveu só o
	// último elemento; tudo antes dele veio do cliente e vale tanto quanto o
	// cliente. (No Fly o último é o IP do app: por isso lá se usa o Header.)
	cadeia := strings.Join(linhas, ",")
	if cadeia == "" {
		return direto
	}
	partes := strings.Split(cadeia, ",")
	ultimo, ok := normalizar(strings.TrimSpace(partes[len(partes)-1]))
	if !ok {
		// Cabeçalho ilegível não pode virar chave de balde nem hash de
		// origem: cai no endereço que a conexão realmente tem.
		return direto
	}
	return ultimo
}

// peloCabecalho lê o cabeçalho de cliente de um valor só (Fly-Client-IP).
func peloCabecalho(r *http.Request, p Proxies, direto string) string {
	valor := strings.TrimSpace(r.Header.Get(p.Header))
	origem, err := netip.ParseAddr(direto)
	doProxy := err == nil && confiavel(origem, p.Trusted)
	if valor == "" {
		if doProxy {
			avisoSemCabecalho.avisar(msgSemCabecalho, direto, p.Header)
		}
		return direto
	}
	if !doProxy {
		// O cabeçalho chegou, mas não de um proxy da lista. Ou é alguém
		// forjando, ou é TRUSTED_PROXIES errado; o aviso diz qual é o
		// endereço que a conexão tem, que é o que falta para configurar.
		avisoDeProxy.avisar(msgForaDaLista, direto, p.Header)
		return direto
	}
	ip, ok := normalizar(valor)
	if !ok {
		// Do proxy, mas ilegível: o valor não vai para o log (pode ser
		// qualquer coisa que o cliente pôs num cabeçalho de mesmo nome).
		avisoSemCabecalho.avisar(msgSemCabecalho, direto, p.Header)
		return direto
	}
	return ip
}

// normalizar é a forma única de um IP que sai de ClientIP: IPv4 mapeado em
// IPv6 vira o IPv4, e endereço com zona é recusado (uma zona num cabeçalho é
// texto do cliente, não um endereço de onde a requisição possa ter vindo).
func normalizar(s string) (string, bool) {
	ip, err := netip.ParseAddr(s)
	if err != nil || ip.Zone() != "" {
		return "", false
	}
	return ip.Unmap().String(), true
}

const (
	msgForaDaLista  = "cabeçalho de IP do cliente ignorado: a conexão não vem de TRUSTED_PROXIES"
	msgSemCabecalho = "cabeçalho de IP do cliente ausente ou ilegível numa conexão de TRUSTED_PROXIES: " +
		"confira o CLIENT_IP_HEADER; estes clientes dividem o balde do proxy"
)

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

// avisoSemCabecalho é o outro lado: a conexão vem de um proxy confiável, mas
// sem o cabeçalho, ou com um valor ilegível. Limite próprio, para um aviso
// não calar o outro.
var avisoSemCabecalho = &aviso{agora: time.Now, intervalo: time.Minute}

// aviso limita um log de configuração de proxy a um por intervalo. Com
// TRUSTED_PROXIES ou CLIENT_IP_HEADER errado, TODA requisição cairia aqui.
type aviso struct {
	mu        sync.Mutex
	ultimo    time.Time
	agora     func() time.Time
	intervalo time.Duration
}

func (a *aviso) avisar(msg, remoto, cabecalho string) {
	a.mu.Lock()
	agora := a.agora()
	if !a.ultimo.IsZero() && agora.Sub(a.ultimo) < a.intervalo {
		a.mu.Unlock()
		return
	}
	a.ultimo = agora
	a.mu.Unlock()
	slog.Warn(msg, "cabecalho", cabecalho, "remote_addr", remoto)
}
