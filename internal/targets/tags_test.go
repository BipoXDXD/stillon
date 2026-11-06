package targets

import (
	"slices"
	"strings"
	"testing"
)

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
