package targets

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
)

func valido() fields {
	return fields{
		Name: "API", URL: "https://api.exemplo.com/health", Method: "GET",
		IntervalSec: 60, TimeoutMS: 10000, ExpectedStatus: 200,
	}
}

func ptr[T any](v T) *T { return &v }

func TestValidate(t *testing.T) {
	cases := []struct {
		nome   string
		mexer  func(*fields)
		campo  string // "" = válido
		motivo string
	}{
		{"válido", func(f *fields) {}, "", ""},
		{"método em minúscula é normalizado", func(f *fields) { f.Method = "get" }, "", ""},

		{"nome vazio", func(f *fields) { f.Name = "   " }, "name", ReasonRequired},
		{"nome com 101 caracteres", func(f *fields) { f.Name = strings.Repeat("á", 101) }, "name", ReasonTooLong},
		{"nome com 100 caracteres", func(f *fields) { f.Name = strings.Repeat("á", 100) }, "", ""},
		{"nome com controle", func(f *fields) { f.Name = "a\x00b" }, "name", ReasonInvalid},
		{"nome com inversão bidi", func(f *fields) { f.Name = "a\u202eb" }, "name", ReasonInvalid},
		{"nome com espaço de largura zero", func(f *fields) { f.Name = "a\u200bb" }, "name", ReasonInvalid},
		{"nome com acento", func(f *fields) { f.Name = "Página de preço" }, "", ""},
		{"nome com emoji de profissão (ZWJ)", func(f *fields) { f.Name = "Equipe \U0001F469\U0000200D\U0001F4BB" }, "", ""},

		{"url vazia", func(f *fields) { f.URL = "" }, "url", ReasonRequired},
		{"url ftp", func(f *fields) { f.URL = "ftp://exemplo.com" }, "url", ReasonInvalid},
		{"url com credencial", func(f *fields) { f.URL = "https://u:p@exemplo.com" }, "url", ReasonInvalid},
		{"url longa demais", func(f *fields) { f.URL = "https://exemplo.com/" + strings.Repeat("a", 2048) }, "url", ReasonTooLong},
		{"url loopback literal", func(f *fields) { f.URL = "http://127.0.0.1:8080/" }, "url", ReasonForbidden},
		{"url loopback IPv6", func(f *fields) { f.URL = "http://[::1]/" }, "url", ReasonForbidden},
		{"url privada", func(f *fields) { f.URL = "http://10.0.0.5/admin" }, "url", ReasonForbidden},
		{"url metadata", func(f *fields) { f.URL = "http://169.254.169.254/latest/" }, "url", ReasonForbidden},
		{"url localhost", func(f *fields) { f.URL = "http://localhost:9000/" }, "url", ReasonForbidden},
		{"url LOCALHOST. com ponto", func(f *fields) { f.URL = "http://LOCALHOST./" }, "url", ReasonForbidden},
		{"url sub.localhost", func(f *fields) { f.URL = "http://app.localhost/" }, "url", ReasonForbidden},
		{"url IP público literal", func(f *fields) { f.URL = "http://8.8.8.8/" }, "", ""},
		{"url sem host", func(f *fields) { f.URL = "http://:8080/" }, "url", ReasonInvalid},
		// IPv4 fora da forma canônica: o resolver do sistema (cgo) o converte
		// em IP, e o Go puro o manda ao DNS. Só a forma a.b.c.d é aceita.
		{"url IP decimal", func(f *fields) { f.URL = "http://2130706433/" }, "url", ReasonInvalid},
		{"url IP hexadecimal", func(f *fields) { f.URL = "http://0x7f.1/" }, "url", ReasonInvalid},
		{"url IP octal", func(f *fields) { f.URL = "http://017700000001/" }, "url", ReasonInvalid},
		{"url IP abreviado", func(f *fields) { f.URL = "http://127.1/" }, "url", ReasonInvalid},
		{"url IP com zero à esquerda", func(f *fields) { f.URL = "http://0177.0.0.1/" }, "url", ReasonInvalid},
		{"url IP público decimal", func(f *fields) { f.URL = "http://134744072/" }, "url", ReasonInvalid},
		{"url com dígito no meio do nome", func(f *fields) { f.URL = "http://api2.exemplo.com/" }, "", ""},
		{"url com hífen e número no rótulo", func(f *fields) { f.URL = "http://host-1.exemplo.com/" }, "", ""},
		{"url com TLD alfanumérico", func(f *fields) { f.URL = "http://api.v2/" }, "", ""},
		{"url com ponto final", func(f *fields) { f.URL = "http://exemplo.com./" }, "", ""},
		{"url IP público com ponto final", func(f *fields) { f.URL = "http://8.8.8.8./" }, "", ""},
		{"url loopback com ponto final", func(f *fields) { f.URL = "http://127.0.0.1./" }, "url", ReasonForbidden},
		{"url IP decimal com ponto final", func(f *fields) { f.URL = "http://2130706433./" }, "url", ReasonInvalid},
		{"url IPv6 público literal", func(f *fields) { f.URL = "http://[2606:4700:4700::1111]/" }, "", ""},
		{"url IP hexadecimal maiúsculo", func(f *fields) { f.URL = "http://0X7F.1/" }, "url", ReasonInvalid},

		{"método DELETE", func(f *fields) { f.Method = "DELETE" }, "method", ReasonInvalid},

		{"intervalo 29", func(f *fields) { f.IntervalSec = 29 }, "interval_sec", ReasonOutOfRange},
		{"intervalo 86401", func(f *fields) { f.IntervalSec = 86401 }, "interval_sec", ReasonOutOfRange},
		{"timeout 999", func(f *fields) { f.TimeoutMS = 999 }, "timeout_ms", ReasonOutOfRange},
		{"timeout 30001", func(f *fields) { f.TimeoutMS = 30001 }, "timeout_ms", ReasonOutOfRange},
		// Review Focus 5 (D3): o jitter de −10% antecipa a próxima sonda
		// para 27s num intervalo de 30s.
		{"timeout em 90% do intervalo", func(f *fields) { f.IntervalSec = 30; f.TimeoutMS = 27000 }, "timeout_ms", ReasonOutOfRange},
		{"timeout logo abaixo de 90%", func(f *fields) { f.IntervalSec = 30; f.TimeoutMS = 26999 }, "", ""},

		{"status 99", func(f *fields) { f.ExpectedStatus = 99 }, "expected_status", ReasonOutOfRange},
		{"status 600", func(f *fields) { f.ExpectedStatus = 600 }, "expected_status", ReasonOutOfRange},
		// C2 (Plano 10): 1xx o net/http consome, e 3xx o cliente segue; os
		// dois nunca chegam à comparação, e o alvo ficaria down para sempre.
		{"status 100", func(f *fields) { f.ExpectedStatus = 100 }, "expected_status", ReasonOutOfRange},
		{"status 199", func(f *fields) { f.ExpectedStatus = 199 }, "expected_status", ReasonOutOfRange},
		{"status 200", func(f *fields) { f.ExpectedStatus = 200 }, "", ""},
		{"status 299", func(f *fields) { f.ExpectedStatus = 299 }, "", ""},
		{"status 300", func(f *fields) { f.ExpectedStatus = 300 }, "expected_status", ReasonOutOfRange},
		{"status 301", func(f *fields) { f.ExpectedStatus = 301 }, "expected_status", ReasonOutOfRange},
		{"status 399", func(f *fields) { f.ExpectedStatus = 399 }, "expected_status", ReasonOutOfRange},
		{"status 400", func(f *fields) { f.ExpectedStatus = 400 }, "", ""},
		{"status 404", func(f *fields) { f.ExpectedStatus = 404 }, "", ""},
		{"status 599", func(f *fields) { f.ExpectedStatus = 599 }, "", ""},

		{"limiar 0", func(f *fields) { f.DegradedThresholdMS = ptr[int32](0) }, "degraded_threshold_ms", ReasonOutOfRange},
		{"limiar 60001", func(f *fields) { f.DegradedThresholdMS = ptr[int32](60001) }, "degraded_threshold_ms", ReasonOutOfRange},
		{"limiar nulo desativa", func(f *fields) { f.DegradedThresholdMS = nil }, "", ""},

		{"header permitido", func(f *fields) { f.Headers = map[string]string{"Authorization": "Bearer x"} }, "", ""},
		{"header Host", func(f *fields) { f.Headers = map[string]string{"host": "interno"} }, "headers", ReasonForbidden},
		{"header com espaço no nome", func(f *fields) { f.Headers = map[string]string{"X Bad": "1"} }, "headers", ReasonInvalid},
		{"header com CRLF no valor", func(f *fields) { f.Headers = map[string]string{"X-A": "1\r\nX-Injetado: 2"} }, "headers", ReasonInvalid},
		{"header duplicado por caixa", func(f *fields) { f.Headers = map[string]string{"x-a": "1", "X-A": "2"} }, "headers", ReasonInvalid},
		// Dois problemas em headers diferentes: o motivo precisa sair do
		// primeiro nome em ordem, não da ordem aleatória do mapa.
		{"headers com dois problemas", func(f *fields) { f.Headers = map[string]string{"Host": "x", "X-A": "1\r\n2"} }, "headers", ReasonForbidden},
		{"header valor longo", func(f *fields) { f.Headers = map[string]string{"X-A": strings.Repeat("a", 1025)} }, "headers", ReasonTooLong},
		{"21 headers", func(f *fields) {
			f.Headers = map[string]string{}
			for i := range 21 {
				f.Headers["X-H"+strings.Repeat("a", i)] = "1"
			}
		}, "headers", ReasonTooLong},
		{"headers somando mais de 8 KiB", func(f *fields) {
			f.Headers = map[string]string{}
			for i := range 9 {
				f.Headers["X-H"+strings.Repeat("a", i)] = strings.Repeat("v", 1000)
			}
		}, "headers", ReasonTooLong},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := valido()
			c.mexer(&f)
			err := validate(&f)
			if c.campo == "" {
				if err != nil {
					t.Fatalf("queria válido, deu %+v", err.Fields)
				}
				return
			}
			if err == nil {
				t.Fatalf("queria erro em %s (%s), passou", c.campo, c.motivo)
			}
			for _, fe := range err.Fields {
				if fe.Field == c.campo && fe.Reason == c.motivo {
					return
				}
			}
			t.Errorf("queria %s/%s, veio %+v", c.campo, c.motivo, err.Fields)
		})
	}
}

// Nomes de header chegam canonizados ao sondador: "authorization" e
// "Authorization" são o mesmo cabeçalho, e só um pode sobrar.
func TestValidateCanonicalizesHeaderNames(t *testing.T) {
	f := valido()
	f.Headers = map[string]string{"x-api-key": "k"}
	if err := validate(&f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Headers["X-Api-Key"]; !ok || len(f.Headers) != 1 {
		t.Errorf("headers = %v, queria só X-Api-Key", f.Headers)
	}
}

// B1 (Plano 10): a origem é esquema, host e porta, na regra do navegador
// (WHATWG): esquema e host sem diferença de caixa, e a porta padrão do
// esquema igual a porta nenhuma. Caminho, consulta e fragmento não contam.
func TestSameOrigin(t *testing.T) {
	cases := []struct {
		a, b string
		quer bool
	}{
		{"https://api.exemplo.com/health", "https://api.exemplo.com/v2/health?x=1#y", true},
		{"https://API.Exemplo.com/", "https://api.exemplo.com/", true},
		{"HTTPS://api.exemplo.com/", "https://api.exemplo.com/", true},
		{"https://api.exemplo.com/", "https://api.exemplo.com:443/", true},
		{"http://api.exemplo.com/", "http://api.exemplo.com:80/x", true},
		{" https://api.exemplo.com/ ", "https://api.exemplo.com/x", true},
		{"http://[2001:DB8::1]:8080/", "http://[2001:db8::1]:8080/x", true},

		{"https://api.exemplo.com/", "https://atacante.example/", false},
		{"https://api.exemplo.com/", "https://outra.api.exemplo.com/", false},
		{"https://api.exemplo.com/", "https://api.exemplo.com:8443/", false},
		{"https://api.exemplo.com/", "http://api.exemplo.com/", false},
		{"http://api.exemplo.com/", "https://api.exemplo.com/", false},
		{"http://api.exemplo.com:443/", "https://api.exemplo.com/", false},
		{"https://api.exemplo.com/", "://quebrada", false},
		{"https://api.exemplo.com/", "", false},

		// Q1 da revisão do Plano 10: o host é comparado como o net/http o
		// disca (idna.Lookup.ToASCII). strings.ToLower dizia "mesma origem"
		// para os dois primeiros, mas o transporte disca xn--strae-oqa.de
		// num e strasse.de no outro, que é outro domínio registrável.
		{"https://straße.de/", "https://STRAẞE.de/", false},
		{"https://straẞe.de/", "https://strasse.de/x", true},
		{"https://straße.de/", "https://xn--strae-oqa.de/x", true},
		{"https://BÜCHER.example/", "https://bücher.example/x", true},
		// Um host que não converte não é a origem de ninguém, nem dele mesmo.
		{"https://-é.de/", "https://-é.de/x", false},
	}
	for _, c := range cases {
		if got := SameOrigin(c.a, c.b); got != c.quer {
			t.Errorf("SameOrigin(%q, %q) = %v, queria %v", c.a, c.b, got, c.quer)
		}
		if got := SameOrigin(c.b, c.a); got != c.quer {
			t.Errorf("SameOrigin(%q, %q) = %v, queria %v (a regra é simétrica)", c.b, c.a, got, c.quer)
		}
	}
}

// O SameOrigin concorda com o que o transporte do net/http de fato disca
// (Q1 da revisão do Plano 10): para cada par de hosts IDN, "mesma origem"
// se e só se as duas URLs discam o mesmo endereço. Prende o perfil do
// idna.Lookup ao do net/http: se um dos dois mudar, este teste vê.
func TestSameOriginAgreesWithTheDialedHost(t *testing.T) {
	discado := func(u string) string {
		t.Helper()
		var addr string
		tr := &http.Transport{DialContext: func(_ context.Context, _, a string) (net.Conn, error) {
			addr = a
			return nil, errors.New("só anotando")
		}}
		resp, err := (&http.Client{Transport: tr}).Get(u)
		if err == nil {
			resp.Body.Close()
		}
		if addr == "" {
			t.Fatalf("o transporte não discou %q: %v", u, err)
		}
		return addr
	}
	pares := [][2]string{
		{"https://straße.de/", "https://STRAẞE.de/"},
		{"https://straẞe.de/", "https://strasse.de/"},
		{"https://straße.de/", "https://xn--strae-oqa.de/"},
		{"https://BÜCHER.example/", "https://bücher.example/"},
		{"https://Ⅸ.example/", "https://ix.example/"},
	}
	for _, p := range pares {
		mesmo := discado(p[0]) == discado(p[1])
		if got := SameOrigin(p[0], p[1]); got != mesmo {
			t.Errorf("SameOrigin(%q, %q) = %v, mas o transporte disca %q e %q", p[0], p[1], got, discado(p[0]), discado(p[1]))
		}
	}
}
