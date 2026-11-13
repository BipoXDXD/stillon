package targets

import (
	"slices"
	"strings"
	"testing"
)

func TestParseJSONPointer(t *testing.T) {
	cases := []struct {
		ponteiro string
		tokens   []string // nil = erro
	}{
		{"/a", []string{"a"}},
		{"/a/0", []string{"a", "0"}},
		{"/a~1b", []string{"a/b"}},
		{"/m~0n", []string{"m~n"}},
		// "~01" é o escape ~0 seguido de "1": vira "~1", nunca "/" (RFC 6901 §4).
		{"/~01", []string{"~1"}},
		{"/", []string{""}},
		{"/a//b", []string{"a", "", "b"}},
		{"a", nil},
		{"", nil},
		{"/a~2", nil},
		{"/a~", nil},
	}
	for _, c := range cases {
		t.Run(c.ponteiro, func(t *testing.T) {
			tokens, err := ParseJSONPointer(c.ponteiro)
			if c.tokens == nil {
				if err == nil {
					t.Fatalf("queria erro, veio %q", tokens)
				}
				return
			}
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if !slices.Equal(tokens, c.tokens) {
				t.Errorf("tokens = %q, queria %q", tokens, c.tokens)
			}
		})
	}
}

func regra(modo ContentMode, valor, ponteiro string) *ContentRule {
	return &ContentRule{Mode: modo, Value: valor, JSONPointer: ponteiro}
}

func TestValidateContentRule(t *testing.T) {
	cases := []struct {
		nome   string
		mexer  func(*fields)
		campo  string // "" = válido
		motivo string
	}{
		{"sem regra", func(f *fields) {}, "", ""},
		{"contains com valor", func(f *fields) { f.ContentRule = regra(ContentContains, "ok", "") }, "", ""},
		{"not_contains com valor", func(f *fields) { f.ContentRule = regra(ContentNotContains, "erro", "") }, "", ""},
		{"json_equals com ponteiro", func(f *fields) { f.ContentRule = regra(ContentJSONEquals, "ok", "/status") }, "", ""},
		{"modo desconhecido", func(f *fields) { f.ContentRule = regra("regex", "ok", "") }, "content_rule.mode", ReasonInvalid},
		// Vazio é o "mode": null (ou ausente) da API: obrigatório, como o valor.
		{"modo vazio", func(f *fields) { f.ContentRule = regra("", "ok", "") }, "content_rule.mode", ReasonRequired},
		{"valor vazio", func(f *fields) { f.ContentRule = regra(ContentContains, "", "") }, "content_rule.value", ReasonRequired},
		{"valor com 256 caracteres de 2 bytes", func(f *fields) {
			f.ContentRule = regra(ContentContains, strings.Repeat("é", 256), "")
		}, "", ""},
		{"valor com 257 caracteres", func(f *fields) {
			f.ContentRule = regra(ContentContains, strings.Repeat("é", 257), "")
		}, "content_rule.value", ReasonTooLong},
		{"valor com NUL", func(f *fields) { f.ContentRule = regra(ContentContains, "a\x00b", "") }, "content_rule.value", ReasonInvalid},
		{"valor com tab", func(f *fields) { f.ContentRule = regra(ContentContains, "a\tb", "") }, "content_rule.value", ReasonInvalid},
		{"valor com quebra de linha", func(f *fields) { f.ContentRule = regra(ContentContains, "a\nb", "") }, "content_rule.value", ReasonInvalid},
		{"valor com DEL", func(f *fields) { f.ContentRule = regra(ContentContains, "a\x7fb", "") }, "content_rule.value", ReasonInvalid},
		{"valor com controle C1", func(f *fields) { f.ContentRule = regra(ContentContains, "a\u0085b", "") }, "content_rule.value", ReasonInvalid},
		{"valor em UTF-8 inválido", func(f *fields) { f.ContentRule = regra(ContentContains, "a\xffb", "") }, "content_rule.value", ReasonInvalid},
		{"json_equals sem ponteiro", func(f *fields) { f.ContentRule = regra(ContentJSONEquals, "ok", "") }, "content_rule.json_pointer", ReasonRequired},
		{"ponteiro sem barra inicial", func(f *fields) { f.ContentRule = regra(ContentJSONEquals, "ok", "status") }, "content_rule.json_pointer", ReasonInvalid},
		{"ponteiro com escape ~2", func(f *fields) { f.ContentRule = regra(ContentJSONEquals, "ok", "/a~2") }, "content_rule.json_pointer", ReasonInvalid},
		{"ponteiro com controle", func(f *fields) { f.ContentRule = regra(ContentJSONEquals, "ok", "/a\x00") }, "content_rule.json_pointer", ReasonInvalid},
		{"ponteiro em contains", func(f *fields) { f.ContentRule = regra(ContentContains, "ok", "/status") }, "content_rule.json_pointer", ReasonInvalid},
		{"ponteiro com 256 caracteres", func(f *fields) {
			f.ContentRule = regra(ContentJSONEquals, "ok", "/"+strings.Repeat("a", 255))
		}, "", ""},
		{"ponteiro com 257 caracteres", func(f *fields) {
			f.ContentRule = regra(ContentJSONEquals, "ok", "/"+strings.Repeat("a", 256))
		}, "content_rule.json_pointer", ReasonTooLong},
		{"método HEAD com regra", func(f *fields) {
			f.Method = "head"
			f.ContentRule = regra(ContentContains, "ok", "")
		}, "content_rule", ReasonForbidden},
		{"header accept-encoding do dono com regra", func(f *fields) {
			f.Headers = map[string]string{"accept-encoding": "gzip"}
			f.ContentRule = regra(ContentContains, "ok", "")
		}, "content_rule", ReasonForbidden},
		{"header range do dono com regra", func(f *fields) {
			f.Headers = map[string]string{"rAnGe": "bytes=0-1023"}
			f.ContentRule = regra(ContentContains, "ok", "")
		}, "content_rule", ReasonForbidden},
		{"método HEAD sem regra", func(f *fields) { f.Method = "HEAD" }, "", ""},
		{"header range sem regra", func(f *fields) {
			f.Headers = map[string]string{"range": "bytes=0-1023"}
		}, "", ""},
		{"header accept-encoding sem regra", func(f *fields) {
			f.Headers = map[string]string{"accept-encoding": "gzip"}
		}, "", ""},
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
			want := []FieldError{{Field: c.campo, Reason: c.motivo}}
			if !slices.Equal(err.Fields, want) {
				t.Errorf("erros = %+v, queria %+v", err.Fields, want)
			}
		})
	}
}

// Se normalizeHeaders recusa os headers, os nomes seguem na caixa do dono, e
// a recusa da regra com Range ainda tem de valer.
func TestValidateContentRuleSeesRangeInRefusedHeaders(t *testing.T) {
	exigirRecusaComHeadersRecusados(t, "rAnGe", "bytes=0-1023")
}

// Idem para o Accept-Encoding: com os headers aceitos, a canonização de
// normalizeHeaders esconderia a comparação sem caixa.
func TestValidateContentRuleSeesAcceptEncodingInRefusedHeaders(t *testing.T) {
	exigirRecusaComHeadersRecusados(t, "aCcEpT-eNcOdInG", "gzip")
}

// exigirRecusaComHeadersRecusados soma ao header do dono um valor longo demais,
// que faz normalizeHeaders recusar tudo, e exige content_rule forbidden.
func exigirRecusaComHeadersRecusados(t *testing.T, nome, valor string) {
	t.Helper()
	f := valido()
	f.Headers = map[string]string{nome: valor, "x-longo": strings.Repeat("a", maxHeaderValueBytes+1)}
	f.ContentRule = regra(ContentContains, "ok", "")
	err := validate(&f)
	if err == nil || !slices.Contains(err.Fields, FieldError{Field: "content_rule", Reason: ReasonForbidden}) {
		t.Errorf("erro = %+v, queria content_rule forbidden entre os erros", err)
	}
}

// Trocar a regra é "mudou a sonda", como URL e método; reenviar a mesma
// regra não é.
func TestPatchApplyContentRule(t *testing.T) {
	gravada := regra(ContentContains, "ok", "")
	cases := []struct {
		nome       string
		patch      Patch
		regraFinal *ContentRule
		mudou      bool
	}{
		{"sem tocar na regra", Patch{}, gravada, false},
		{"mesma regra reenviada", Patch{SetContentRule: true, ContentRule: regra(ContentContains, "ok", "")}, gravada, false},
		{"outro valor", Patch{SetContentRule: true, ContentRule: regra(ContentContains, "pronto", "")}, regra(ContentContains, "pronto", ""), true},
		{"outro modo", Patch{SetContentRule: true, ContentRule: regra(ContentNotContains, "ok", "")}, regra(ContentNotContains, "ok", ""), true},
		{"limpar a regra", Patch{SetContentRule: true}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := valido()
			f.ContentRule = regra(ContentContains, "ok", "")
			enabled := true
			mudou := c.patch.apply(&f, &enabled)
			if mudou != c.mudou {
				t.Errorf("mudouASonda = %v, queria %v", mudou, c.mudou)
			}
			if !regrasIguais(f.ContentRule, c.regraFinal) {
				t.Errorf("regra = %+v, queria %+v", f.ContentRule, c.regraFinal)
			}
		})
	}
}

// Limpar a regra num alvo sem regra não é mudança.
func TestPatchApplyLimparRegraAusente(t *testing.T) {
	f := valido()
	enabled := true
	if (Patch{SetContentRule: true}).apply(&f, &enabled) {
		t.Error("mudouASonda = true ao limpar uma regra que não existia")
	}
}

func regrasIguais(a, b *ContentRule) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Fora de json_equals o ponteiro precisa sair nulo: o CHECK
// targets_content_rule_shape recusa ponteiro em contains e not_contains.
func TestColunasDaRegraIdaEVolta(t *testing.T) {
	for _, r := range []*ContentRule{
		nil,
		regra(ContentContains, "ok", ""),
		regra(ContentNotContains, "erro", ""),
		regra(ContentJSONEquals, `"ok"`, "/status"),
	} {
		modo, valor, ponteiro := colunasDaRegra(r)
		if r == nil && (modo != nil || valor != nil || ponteiro != nil) {
			t.Errorf("sem regra: colunas %v %v %v, queria as três nulas", modo, valor, ponteiro)
		}
		if r != nil && (ponteiro == nil) != (r.Mode != ContentJSONEquals) {
			t.Errorf("%s: ponteiro nulo = %v, queria nulo só fora de json_equals", r.Mode, ponteiro == nil)
		}
		if volta := RuleFromColumns(modo, valor, ponteiro); !regrasIguais(volta, r) {
			t.Errorf("ida e volta de %+v devolveu %+v", r, volta)
		}
	}
}
