package targets

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// As fronteiras do cadastro, nos dois lados: o TestValidate cobre o valor logo
// fora de cada faixa, e estes casos prendem o último valor aceito.
func TestValidateAceitaOsLimitesExatos(t *testing.T) {
	cases := []struct {
		nome  string
		mexer func(*fields)
	}{
		{"intervalo 30", func(f *fields) { f.IntervalSec = 30; f.TimeoutMS = 1000 }},
		{"intervalo 86400", func(f *fields) { f.IntervalSec = 86400 }},
		{"timeout 1000", func(f *fields) { f.TimeoutMS = 1000 }},
		{"timeout 30000", func(f *fields) { f.TimeoutMS = 30000 }},
		{"URL com 2048 bytes", func(f *fields) { f.URL = urlDeTamanho(2048) }},
		{"limiar 1", func(f *fields) { f.DegradedThresholdMS = ptr[int32](1) }},
		{"limiar 60000", func(f *fields) { f.DegradedThresholdMS = ptr[int32](60000) }},
		{"20 headers", func(f *fields) { f.Headers = headersNumerados(20, 1) }},
		{"valor de header com 1024 bytes", func(f *fields) { f.Headers = map[string]string{"X-A": strings.Repeat("v", 1024)} }},
		{"headers somando 8192 bytes", func(f *fields) { f.Headers = headersNumerados(8, 1019) }},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := valido()
			c.mexer(&f)
			if err := validate(&f); err != nil {
				t.Fatalf("queria válido, deu %+v", err.Fields)
			}
		})
	}
}

// O erro é exatamente o esperado, sem acusação extra em outro campo.
func TestValidateErrosExatos(t *testing.T) {
	cases := []struct {
		nome  string
		mexer func(*fields)
		quer  []FieldError
	}{
		{"URL com 2049 bytes", func(f *fields) { f.URL = urlDeTamanho(2049) }, []FieldError{{"url", ReasonTooLong}}},
		{"headers somando 8193 bytes, contando os nomes", func(f *fields) {
			f.Headers = headersNumerados(8, 1019)
			f.Headers["X-H00"] = strings.Repeat("v", 1020)
		}, []FieldError{{"headers", ReasonTooLong}}},
		// D3: com o intervalo já errado, o timeout não é acusado por ele.
		{"intervalo inválido não acusa o timeout", func(f *fields) { f.IntervalSec = 10; f.TimeoutMS = 10000 }, []FieldError{{"interval_sec", ReasonOutOfRange}}},
		// Todos os erros numa passada: o cliente corrige tudo de uma vez.
		{"acumula os erros de todos os campos", func(f *fields) {
			f.Name, f.URL, f.Method = "", "", "PUT"
			f.IntervalSec, f.TimeoutMS, f.ExpectedStatus = 29, 999, 99
			f.DegradedThresholdMS = ptr[int32](0)
			f.Headers = map[string]string{"Host": "x"}
		}, []FieldError{
			{"name", ReasonRequired},
			{"url", ReasonRequired},
			{"method", ReasonInvalid},
			{"interval_sec", ReasonOutOfRange},
			{"timeout_ms", ReasonOutOfRange},
			{"expected_status", ReasonOutOfRange},
			{"degraded_threshold_ms", ReasonOutOfRange},
			{"headers", ReasonForbidden},
		}},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := valido()
			c.mexer(&f)
			err := validate(&f)
			if err == nil {
				t.Fatalf("passou; queria %+v", c.quer)
			}
			if !slices.Equal(err.Fields, c.quer) {
				t.Errorf("erros = %+v, queria %+v", err.Fields, c.quer)
			}
		})
	}
}

// D4: só GET, HEAD e POST; PUT e DELETE sem corpo seriam armadilha.
func TestValidateMetodos(t *testing.T) {
	cases := []struct {
		entrada string
		quer    string // "" = recusado
	}{
		{"GET", "GET"},
		{"HEAD", "HEAD"},
		{"POST", "POST"},
		{"head", "HEAD"},
		{"Post", "POST"},
		{"PUT", ""},
		{"PATCH", ""},
		{"DELETE", ""},
		{"OPTIONS", ""},
		{"TRACE", ""},
		{"CONNECT", ""},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.entrada, func(t *testing.T) {
			f := valido()
			f.Method = c.entrada
			err := validate(&f)
			if c.quer == "" {
				if err == nil || !slices.Equal(err.Fields, []FieldError{{"method", ReasonInvalid}}) {
					t.Fatalf("erro = %+v, queria method/invalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("recusado: %+v", err.Fields)
			}
			if f.Method != c.quer {
				t.Errorf("Method = %q, queria %q", f.Method, c.quer)
			}
		})
	}
}

// Os cabeçalhos do transporte, em qualquer caixa: definir um deles quebraria o
// enquadramento HTTP, a conexão ou a noção de destino.
func TestValidateCabecalhosDoTransporteSaoProibidos(t *testing.T) {
	for _, nome := range []string{
		"Host", "Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive",
		"Upgrade", "TE", "Trailer", "Expect", "Proxy-Authorization", "Proxy-Connection",
		"host", "transfer-encoding", "PROXY-AUTHORIZATION",
	} {
		t.Run(nome, func(t *testing.T) {
			f := valido()
			f.Headers = map[string]string{nome: "x"}
			err := validate(&f)
			if err == nil || !slices.Equal(err.Fields, []FieldError{{"headers", ReasonForbidden}}) {
				t.Fatalf("erro = %+v, queria headers/forbidden", err)
			}
		})
	}
}

func TestValidateNomesEValoresDeHeader(t *testing.T) {
	cases := []struct {
		nome   string
		header string
		valor  string
		motivo string // "" = válido
	}{
		{"nome vazio", "", "x", ReasonInvalid},
		{"nome com dígito", "X-Versao-2", "x", ""},
		{"nome com todos os símbolos do token", "X!#$%&'*+-.^_`|~y", "x", ""},
		{"nome com dois-pontos", "X:A", "x", ReasonInvalid},
		{"nome com acento", "X-Ação", "x", ReasonInvalid},
		{"valor com tab", "X-A", "a\tb", ""},
		{"valor com espaço (0x20)", "X-A", "a b", ""},
		{"valor com til (0x7e)", "X-A", "a~b", ""},
		{"valor com 0x1f", "X-A", "a\x1fb", ReasonInvalid},
		{"valor com DEL (0x7f)", "X-A", "a\x7fb", ReasonInvalid},
		{"valor com NUL", "X-A", "a\x00b", ReasonInvalid},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := valido()
			f.Headers = map[string]string{c.header: c.valor}
			err := validate(&f)
			if c.motivo == "" {
				if err != nil {
					t.Fatalf("recusado: %+v", err.Fields)
				}
				return
			}
			if err == nil || !slices.Equal(err.Fields, []FieldError{{"headers", c.motivo}}) {
				t.Fatalf("erro = %+v, queria headers/%s", err, c.motivo)
			}
		})
	}
}

// Sem headers é nil, e não um mapa vazio: é o que grava NULL na coluna.
func TestValidateHeadersVaziosViramNil(t *testing.T) {
	f := valido()
	f.Headers = map[string]string{}
	if err := validate(&f); err != nil {
		t.Fatal(err)
	}
	if f.Headers != nil {
		t.Errorf("Headers = %#v, queria nil", f.Headers)
	}
}

func TestValidateNormalizaURLETags(t *testing.T) {
	f := valido()
	f.URL = "  https://exemplo.com/a\t"
	f.Tags = []string{"Fluxo-E2E"}
	if err := validate(&f); err != nil {
		t.Fatalf("recusado: %+v", err.Fields)
	}
	if f.URL != "https://exemplo.com/a" {
		t.Errorf("URL = %q, queria sem espaço nas pontas", f.URL)
	}
	if !slices.Equal(f.Tags, []string{"fluxo-e2e"}) {
		t.Errorf("Tags = %v, queria [fluxo-e2e]", f.Tags)
	}
}

// Hosts que o resolver do sistema lê como IP, ou que resolvem para loopback.
func TestValidateHostsDeFronteira(t *testing.T) {
	cases := []struct {
		url    string
		motivo string // "" = válido
	}{
		{"http://APP.LOCALHOST/", ReasonForbidden},
		{"http://1.0x7f/", ReasonInvalid},
		{"http://1.0xff/", ReasonInvalid},
		{"http://1.0XFF/", ReasonInvalid},
		{"http://1.0x/", ReasonInvalid},
		{"http://a.0xg/", ""},
		{"http://a.b0/", ""},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			f := valido()
			f.URL = c.url
			err := validate(&f)
			if c.motivo == "" {
				if err != nil {
					t.Fatalf("recusado: %+v", err.Fields)
				}
				return
			}
			if err == nil || !slices.Equal(err.Fields, []FieldError{{"url", c.motivo}}) {
				t.Fatalf("erro = %+v, queria url/%s", err, c.motivo)
			}
		})
	}
}

// Um alvo gravado antes da recusa do IDNA instável continua administrável; a
// mesma URL num cadastro novo, ou trocada por outra instável, é recusada.
func TestValidateURLGravadaInstavel(t *testing.T) {
	const instavel = "https://STRAẞE.de/"
	cases := []struct {
		nome    string
		gravada string
		url     string
		valido  bool
	}{
		{"cadastro novo", "", instavel, false},
		{"a mesma URL já gravada", instavel, instavel, true},
		{"troca por outra instável", instavel, "https://STRAẞE.de/outra", false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := valido()
			f.URL, f.urlGravada = c.url, c.gravada
			err := validate(&f)
			if c.valido && err != nil {
				t.Fatalf("recusado: %+v", err.Fields)
			}
			if !c.valido && (err == nil || !slices.Equal(err.Fields, []FieldError{{"url", ReasonInvalid}})) {
				t.Fatalf("erro = %+v, queria url/invalid", err)
			}
		})
	}
}

func TestSameOriginSemHost(t *testing.T) {
	if SameOrigin("https:///a", "https:///b") {
		t.Error("duas URLs sem host não têm a origem de ninguém")
	}
}

func TestMudouDeDestino(t *testing.T) {
	cases := []struct {
		nome          string
		antes, depois string
		quer          bool
	}{
		{"mesma origem, outro caminho", "https://a.exemplo.com/x", "https://a.exemplo.com/y", false},
		{"outra origem", "https://a.exemplo.com/", "https://b.exemplo.com/", true},
		{"host instável, outro caminho", "https://straẞe.de/a", "https://straẞe.de/b", true},
		{"host instável, a mesma URL", "https://straẞe.de/a", "https://straẞe.de/a", false},
		{"host instável, só espaço nas pontas", " https://straẞe.de/a ", "https://straẞe.de/a", false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			if got := mudouDeDestino(c.antes, c.depois); got != c.quer {
				t.Errorf("mudouDeDestino(%q, %q) = %v, queria %v", c.antes, c.depois, got, c.quer)
			}
		})
	}
}

// O que cada campo do PATCH faz, e se ele muda O QUE é sondado (D13).
func TestPatchApply(t *testing.T) {
	headers := map[string]string{"Authorization": "Bearer x"}
	novos := map[string]string{"X-Novo": "1"}
	base := func() fields {
		return fields{
			Name: "API", URL: "https://a.exemplo.com/x", Method: "GET",
			IntervalSec: 60, TimeoutMS: 10000, ExpectedStatus: 200,
			DegradedThresholdMS: ptr[int32](2000), Headers: headers, Tags: []string{"prod"},
		}
	}
	cases := []struct {
		nome     string
		patch    Patch
		mudou    bool
		conferir func(*testing.T, fields, bool)
	}{
		{"nome não muda o sondado", Patch{Name: ptr("Novo")}, false, func(t *testing.T, f fields, _ bool) {
			if f.Name != "Novo" {
				t.Errorf("Name = %q", f.Name)
			}
		}},
		{"URL na mesma origem mantém os headers", Patch{URL: ptr("https://a.exemplo.com/y")}, true, func(t *testing.T, f fields, _ bool) {
			if f.URL != "https://a.exemplo.com/y" || !maps.Equal(f.Headers, headers) {
				t.Errorf("URL = %q, Headers = %v", f.URL, f.Headers)
			}
		}},
		{"URL em outra origem apaga os headers", Patch{URL: ptr("https://b.exemplo.com/")}, true, func(t *testing.T, f fields, _ bool) {
			if f.Headers != nil {
				t.Errorf("Headers = %v, queria nil", f.Headers)
			}
		}},
		{"outra origem com headers no PATCH fica com os novos", Patch{URL: ptr("https://b.exemplo.com/"), SetHeaders: true, Headers: novos}, true, func(t *testing.T, f fields, _ bool) {
			if !maps.Equal(f.Headers, novos) {
				t.Errorf("Headers = %v, queria %v", f.Headers, novos)
			}
		}},
		{"só os headers mudam o sondado", Patch{SetHeaders: true, Headers: novos}, true, func(t *testing.T, f fields, _ bool) {
			if !maps.Equal(f.Headers, novos) {
				t.Errorf("Headers = %v, queria %v", f.Headers, novos)
			}
		}},
		{"limpar os headers", Patch{SetHeaders: true}, true, func(t *testing.T, f fields, _ bool) {
			if f.Headers != nil {
				t.Errorf("Headers = %v, queria nil", f.Headers)
			}
		}},
		{"método", Patch{Method: ptr("POST")}, true, func(t *testing.T, f fields, _ bool) {
			if f.Method != "POST" {
				t.Errorf("Method = %q", f.Method)
			}
		}},
		{"intervalo", Patch{IntervalSec: ptr[int32](120)}, true, func(t *testing.T, f fields, _ bool) {
			if f.IntervalSec != 120 {
				t.Errorf("IntervalSec = %d", f.IntervalSec)
			}
		}},
		{"timeout", Patch{TimeoutMS: ptr[int32](5000)}, true, func(t *testing.T, f fields, _ bool) {
			if f.TimeoutMS != 5000 {
				t.Errorf("TimeoutMS = %d", f.TimeoutMS)
			}
		}},
		{"status", Patch{ExpectedStatus: ptr[int32](204)}, true, func(t *testing.T, f fields, _ bool) {
			if f.ExpectedStatus != 204 {
				t.Errorf("ExpectedStatus = %d", f.ExpectedStatus)
			}
		}},
		{"limpar o limiar não muda o sondado", Patch{SetDegradedThreshold: true}, false, func(t *testing.T, f fields, _ bool) {
			if f.DegradedThresholdMS != nil {
				t.Errorf("DegradedThresholdMS = %v, queria nil", *f.DegradedThresholdMS)
			}
		}},
		{"limpar as tags não muda o sondado", Patch{SetTags: true}, false, func(t *testing.T, f fields, _ bool) {
			if f.Tags != nil {
				t.Errorf("Tags = %v, queria nil", f.Tags)
			}
		}},
		{"Enabled", Patch{Enabled: ptr(false)}, false, func(t *testing.T, _ fields, enabled bool) {
			if enabled {
				t.Error("enabled continuou true")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := base()
			enabled := true
			if got := c.patch.apply(&f, &enabled); got != c.mudou {
				t.Errorf("apply devolveu %v, queria %v", got, c.mudou)
			}
			c.conferir(t, f, enabled)
		})
	}
}

// urlDeTamanho devolve uma URL válida com exatamente n bytes.
func urlDeTamanho(n int) string {
	const prefixo = "https://exemplo.com/"
	return prefixo + strings.Repeat("a", n-len(prefixo))
}

// headersNumerados devolve n headers cujos nomes têm 5 bytes (X-H00, X-H01…),
// cada um com um valor de tamanho bytes.
func headersNumerados(n, tamanho int) map[string]string {
	h := make(map[string]string, n)
	for i := range n {
		h[fmt.Sprintf("X-H%02d", i)] = strings.Repeat("v", tamanho)
	}
	return h
}
