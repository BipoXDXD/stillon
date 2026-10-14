package targets

import (
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/BipoXDXD/stillon/internal/prober/safedial"
	"github.com/BipoXDXD/stillon/internal/rotulo"
)

// Motivos de validação. Vocabulário fechado: a camada HTTP os repassa ao
// cliente, e nada que venha do corpo recebido entra neles.
const (
	ReasonRequired   = "required"
	ReasonInvalid    = "invalid"
	ReasonOutOfRange = "out_of_range"
	ReasonTooLong    = "too_long"
	ReasonForbidden  = "forbidden"
)

type FieldError struct {
	Field  string
	Reason string
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string { return "dados do alvo inválidos" }

func (e *ValidationError) add(field, reason string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Reason: reason})
}

// Limites de cadastro. Cada um tem par num CHECK da migration 00004.
const (
	minIntervalSec         = 30
	maxIntervalSec         = 86400
	minTimeoutMS           = 1000
	maxTimeoutMS           = 30000
	maxNameRunes           = 100
	maxURLBytes            = 2048
	maxDegradedThresholdMS = 60000
	maxHeaders             = 20
	maxHeaderValueBytes    = 1024
	maxHeadersTotalBytes   = 8 << 10
)

// D4: não há coluna de corpo, e PUT ou DELETE sem corpo num monitor seriam
// armadilha.
var allowedMethods = []string{"GET", "HEAD", "POST"}

// forbiddenHeaders são os que o transporte controla. Deixar o usuário
// defini-los quebraria o enquadramento HTTP (Content-Length,
// Transfer-Encoding), a conexão (Connection, Upgrade) ou a noção de destino
// (Host), sem nenhum ganho para monitoramento.
var forbiddenHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true,
	"Connection": true, "Keep-Alive": true, "Upgrade": true, "Te": true,
	"Trailer": true, "Expect": true, "Proxy-Authorization": true,
	"Proxy-Connection": true,
}

// fields é o alvo em validação: a união do que Input e Patch conseguem mexer.
type fields struct {
	Name                string
	URL                 string
	Method              string
	IntervalSec         int32
	TimeoutMS           int32
	ExpectedStatus      int32
	DegradedThresholdMS *int32
	Headers             map[string]string
}

// validate confere todos os campos e acumula todos os erros, para que o
// cliente corrija tudo numa passada. Normaliza no lugar: nome e URL sem
// espaço nas pontas, método em maiúscula, nomes de header canônicos.
func validate(f *fields) *ValidationError {
	var v ValidationError

	f.Name = strings.TrimSpace(f.Name)
	switch {
	case f.Name == "":
		v.add("name", ReasonRequired)
	case utf8.RuneCountInString(f.Name) > maxNameRunes:
		v.add("name", ReasonTooLong)
	case rotulo.TemInvisivel(f.Name):
		v.add("name", ReasonInvalid)
	}

	f.URL = strings.TrimSpace(f.URL)
	if motivo := checkURL(f.URL); motivo != "" {
		v.add("url", motivo)
	}

	f.Method = strings.ToUpper(f.Method)
	if !slices.Contains(allowedMethods, f.Method) {
		v.add("method", ReasonInvalid)
	}

	intervaloOK := f.IntervalSec >= minIntervalSec && f.IntervalSec <= maxIntervalSec
	if !intervaloOK {
		v.add("interval_sec", ReasonOutOfRange)
	}
	switch {
	case f.TimeoutMS < minTimeoutMS || f.TimeoutMS > maxTimeoutMS:
		v.add("timeout_ms", ReasonOutOfRange)
	// D3: o jitter de −10% (spec §6.2) pode antecipar a próxima execução
	// para 90% do intervalo. Um timeout acima disso deixaria duas sondas do
	// mesmo alvo em voo ao mesmo tempo. Só se compara com intervalo válido,
	// para não acusar o timeout pelo erro do intervalo.
	case intervaloOK && int64(f.TimeoutMS)*10 >= int64(f.IntervalSec)*9000:
		v.add("timeout_ms", ReasonOutOfRange)
	}

	// C2 (Plano 10): só 2xx e 4xx–5xx podem ser o status final. O 1xx o
	// net/http consome, e o 3xx o cliente segue (até 3 saltos); esperar um
	// deles deixaria o alvo down para sempre, com um incidente que não fecha.
	if s := f.ExpectedStatus; !(s >= 200 && s <= 299) && !(s >= 400 && s <= 599) {
		v.add("expected_status", ReasonOutOfRange)
	}

	if d := f.DegradedThresholdMS; d != nil && (*d < 1 || *d > maxDegradedThresholdMS) {
		v.add("degraded_threshold_ms", ReasonOutOfRange)
	}

	if h, motivo := normalizeHeaders(f.Headers); motivo != "" {
		v.add("headers", motivo)
	} else {
		f.Headers = h
	}

	if len(v.Fields) == 0 {
		return nil
	}
	return &v
}

// checkURL é a recusa antecipada, só para dar resposta útil no cadastro.
// A guarda de verdade é o dial (safedial, spec §5.1): um NOME que resolve
// para IP interno passa daqui e morre lá, e é assim que tem de ser. Resolver
// DNS no cadastro seria exatamente a janela de rebinding que a spec descreve.
func checkURL(raw string) string {
	if raw == "" {
		return ReasonRequired
	}
	if len(raw) > maxURLBytes {
		return ReasonTooLong
	}
	u, err := safedial.ValidateURL(raw)
	if err != nil {
		return ReasonInvalid
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	// "http://:8080/" passa pelo parser com Hostname() vazio: não há destino.
	if host == "" {
		return ReasonInvalid
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if safedial.AllowIP(ip) != nil {
			return ReasonForbidden
		}
		return ""
	}
	// Host que termina em número é IPv4 fora da forma a.b.c.d (2130706433,
	// 0x7f.1, 127.1): o resolver do sistema o lê como IP, o Go puro o manda ao
	// DNS, e o cadastro não tem como saber qual dos dois vai sondá-lo.
	if terminaEmNumero(host) {
		return ReasonInvalid
	}
	// RFC 6761: localhost e todo *.localhost resolvem para loopback.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ReasonForbidden
	}
	return ""
}

// terminaEmNumero é a regra "ends in a number" do WHATWG URL: o último rótulo
// só com dígitos, ou 0x seguido de hexadecimal, faz do host um IPv4.
func terminaEmNumero(host string) bool {
	rotulo := host[strings.LastIndexByte(host, '.')+1:]
	if hexa, ok := strings.CutPrefix(rotulo, "0x"); ok {
		return strings.Trim(hexa, "0123456789abcdef") == ""
	}
	return rotulo != "" && strings.Trim(rotulo, "0123456789") == ""
}

func normalizeHeaders(in map[string]string) (map[string]string, string) {
	if len(in) == 0 {
		return nil, ""
	}
	if len(in) > maxHeaders {
		return nil, ReasonTooLong
	}
	out := make(map[string]string, len(in))
	total := 0
	// Em ordem de nome, não na ordem aleatória do mapa: com dois headers
	// ruins, o motivo devolvido tem de ser sempre o mesmo.
	for _, nome := range slices.Sorted(maps.Keys(in)) {
		valor := in[nome]
		if !validHeaderName(nome) {
			return nil, ReasonInvalid
		}
		canon := http.CanonicalHeaderKey(nome)
		if forbiddenHeaders[canon] {
			return nil, ReasonForbidden
		}
		if _, dup := out[canon]; dup {
			return nil, ReasonInvalid
		}
		if len(valor) > maxHeaderValueBytes {
			return nil, ReasonTooLong
		}
		if !validHeaderValue(valor) {
			return nil, ReasonInvalid
		}
		total += len(canon) + len(valor)
		out[canon] = valor
	}
	if total > maxHeadersTotalBytes {
		return nil, ReasonTooLong
	}
	return out, ""
}

// validHeaderName aceita só token (RFC 9110 §5.6.2).
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
		if !ok {
			return false
		}
	}
	return true
}

// validHeaderValue recusa CR e LF, que injetariam um cabeçalho novo, e os
// demais caracteres de controle, exceto tab.
func validHeaderValue(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// SameOrigin diz se as duas URLs têm a mesma origem, na regra do navegador
// (WHATWG): esquema sem diferença de caixa, o host como o transporte o disca,
// e a porta padrão do esquema (80 no http, 443 no https) igual a porta
// nenhuma. Caminho, consulta e fragmento não contam. Uma URL que não se lê, ou
// cujo host não converte para ASCII, não tem a origem de ninguém.
//
// O host é comparado depois de idna.Lookup.ToASCII, o perfil que o net/http
// (e com ele o safedial) usa para discar e para o SNI (Q1 da revisão do Plano
// 10). Com strings.ToLower, "straße.de" e "STRAẞE.de" davam a mesma origem,
// mas o transporte disca xn--strae-oqa.de num e strasse.de no outro, que é
// outro domínio: o PATCH mantinha os headers e o próximo check os levava ao
// domínio novo.
//
// É a regra do B1 do Plano 10: um PATCH que troca a origem sem mandar
// headers os apaga. O painel a reimplementa com new URL(x).origin para avisar
// antes de salvar, que usa o mesmo UTS 46. Onde as duas ainda divergirem
// (IPv4 em notação não canônica, um host que o navegador aceita e o ToASCII
// recusa), o servidor vê origem diferente e apaga, que é o lado seguro.
func SameOrigin(a, b string) bool {
	oa, ok := origem(a)
	if !ok {
		return false
	}
	ob, ok := origem(b)
	return ok && oa == ob
}

func origem(s string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	host, ok := safedial.DialedHost(u.Hostname())
	if !ok {
		return "", false
	}
	esquema := strings.ToLower(u.Scheme)
	porta := u.Port()
	if esquema == "http" && porta == "80" || esquema == "https" && porta == "443" {
		porta = ""
	}
	return esquema + "://" + host + "|" + porta, true
}
