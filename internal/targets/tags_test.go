package targets

import (
	"slices"
	"strings"
	"testing"
)

func TestParseTag(t *testing.T) {
	cases := []struct {
		nome, entrada, tag, motivo string
	}{
		{"simples", "prod", "prod", ""},
		{"maiúscula vira minúscula", "PROD", "prod", ""},
		{"espaço nas pontas some", "  prod\t", "prod", ""},
		{"acento precomposto", "Produção", "produção", ""},
		{"hífen, sublinhado e dígito", "api-v2_br", "api-v2_br", ""},
		{"24 caracteres", strings.Repeat("á", 24), strings.Repeat("á", 24), ""},

		{"25 caracteres", strings.Repeat("á", 25), "", ReasonTooLong},
		{"vazia", "", "", ReasonInvalid},
		{"só espaço", "   ", "", ReasonInvalid},
		{"espaço no meio", "a b", "", ReasonInvalid},
		{"vírgula", "a,b", "", ReasonInvalid},
		{"NUL", "a\x00b", "", ReasonInvalid},
		{"controle", "a\nb", "", ReasonInvalid},
		{"inversão bidi", "a\u202eb", "", ReasonInvalid},
		{"largura zero", "a\u200bb", "", ReasonInvalid},
		{"acento combinante (forma decomposta)", "produção", "", ReasonInvalid},
		{"barra", "a/b", "", ReasonInvalid},
		{"aspas", `a"b`, "", ReasonInvalid},
		{"percentual", "100%", "", ReasonInvalid},
		{"UTF-8 inválido", "a\xffb", "", ReasonInvalid},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			tag, motivo := ParseTag(c.entrada)
			if tag != c.tag || motivo != c.motivo {
				t.Errorf("ParseTag(%q) = (%q, %q); queria (%q, %q)", c.entrada, tag, motivo, c.tag, c.motivo)
			}
		})
	}
}

func TestNormalizeTags(t *testing.T) {
	oito := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	cases := []struct {
		nome    string
		entrada []string
		saida   []string
		motivo  string
	}{
		{"nil vira lista vazia", nil, []string{}, ""},
		{"vazia", []string{}, []string{}, ""},
		{"ordena", []string{"b", "a"}, []string{"a", "b"}, ""},
		{"duplicata de caixa some", []string{"Prod", "prod", " PROD "}, []string{"prod"}, ""},
		{"oito", oito, oito, ""},
		{"nove", append(slices.Clone(oito), "i"), nil, ReasonOutOfRange},
		// O teto conta o que sobra depois de deduplicar: repetir não é ter mais tags.
		{"nove com uma repetida", append(slices.Clone(oito), "a"), oito, ""},
		{"uma inválida recusa a lista", []string{"ok", "a b"}, nil, ReasonInvalid},
		{"uma longa recusa a lista", []string{"ok", strings.Repeat("a", 25)}, nil, ReasonTooLong},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			saida, motivo := normalizeTags(c.entrada)
			if motivo != c.motivo {
				t.Fatalf("motivo = %q; queria %q", motivo, c.motivo)
			}
			if c.motivo == "" && (saida == nil || !slices.Equal(saida, c.saida)) {
				t.Errorf("saída = %#v; queria %#v", saida, c.saida)
			}
		})
	}
}

func TestValidateRecusaTagsInvalidas(t *testing.T) {
	f := valido()
	f.Tags = []string{"a b"}
	verr := validate(&f)
	if verr == nil || len(verr.Fields) != 1 || verr.Fields[0] != (FieldError{"tags", ReasonInvalid}) {
		t.Fatalf("validate = %+v; queria tags inválidas", verr)
	}
}

func TestParseSummaryFilter(t *testing.T) {
	nove := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	cases := []struct {
		nome          string
		tags, estados []string
		campo, motivo string
		filtro        SummaryFilter
	}{
		{nome: "sem filtro"},
		{nome: "tag normalizada", tags: []string{"PROD"}, filtro: SummaryFilter{Tags: []string{"prod"}}},
		{nome: "todos os estados", estados: []string{"up", "degraded", "down", "unknown", "paused"},
			filtro: SummaryFilter{States: []string{"up", "degraded", "down", "unknown", "paused"}}},
		{nome: "oito tags", tags: nove[:8], filtro: SummaryFilter{Tags: nove[:8]}},

		{nome: "nove tags", tags: nove, campo: "tag", motivo: ReasonOutOfRange},
		{nome: "tag inválida", tags: []string{"a b"}, campo: "tag", motivo: ReasonInvalid},
		{nome: "tag longa", tags: []string{strings.Repeat("a", 25)}, campo: "tag", motivo: ReasonTooLong},
		{nome: "estado fora da allowlist", estados: []string{"up", "pausado"}, campo: "state", motivo: ReasonInvalid},
		{nome: "estado com injeção", estados: []string{"up' OR 1=1"}, campo: "state", motivo: ReasonInvalid},
		{nome: "estados demais", estados: []string{"up", "up", "up", "up", "up", "up"}, campo: "state", motivo: ReasonOutOfRange},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			filtro, err := ParseSummaryFilter(c.tags, c.estados)
			if c.campo != "" {
				if err == nil || len(err.Fields) != 1 || err.Fields[0] != (FieldError{c.campo, c.motivo}) {
					t.Fatalf("erro = %+v; queria %s/%s", err, c.campo, c.motivo)
				}
				return
			}
			if err != nil {
				t.Fatalf("erro inesperado: %+v", err)
			}
			if !slices.Equal(filtro.Tags, c.filtro.Tags) || !slices.Equal(filtro.States, c.filtro.States) {
				t.Errorf("filtro = %+v; queria %+v", filtro, c.filtro)
			}
		})
	}
}

func TestSummaryFilterAccepts(t *testing.T) {
	alvo := Summary{Enabled: true, State: "down", Tags: []string{"api", "prod"}}
	pausado := Summary{Enabled: false, State: "unknown", Tags: []string{"prod"}}
	cases := []struct {
		nome    string
		filtro  SummaryFilter
		s       Summary
		aceitar bool
	}{
		{"sem filtro aceita", SummaryFilter{}, alvo, true},
		{"uma tag", SummaryFilter{Tags: []string{"prod"}}, alvo, true},
		{"todas as tags precisam estar (E)", SummaryFilter{Tags: []string{"prod", "api"}}, alvo, true},
		{"falta uma tag", SummaryFilter{Tags: []string{"prod", "web"}}, alvo, false},
		{"qualquer estado serve (OU)", SummaryFilter{States: []string{"up", "down"}}, alvo, true},
		{"estado diferente", SummaryFilter{States: []string{"up"}}, alvo, false},
		{"tag e estado juntos", SummaryFilter{Tags: []string{"prod"}, States: []string{"up"}}, alvo, false},
		{"pausado é paused", SummaryFilter{States: []string{"paused"}}, pausado, true},
		{"pausado não é unknown", SummaryFilter{States: []string{"unknown"}}, pausado, false},
		{"ativo não é paused", SummaryFilter{States: []string{"paused"}}, alvo, false},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			if got := c.filtro.accepts(c.s); got != c.aceitar {
				t.Errorf("accepts = %v; queria %v", got, c.aceitar)
			}
		})
	}
}
