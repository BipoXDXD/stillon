package rotulo

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

		{"25 caracteres", strings.Repeat("á", 25), "", MotivoTagLonga},
		{"vazia", "", "", MotivoTagInvalida},
		{"só espaço", "   ", "", MotivoTagInvalida},
		{"espaço no meio", "a b", "", MotivoTagInvalida},
		{"vírgula", "a,b", "", MotivoTagInvalida},
		{"NUL", "a\x00b", "", MotivoTagInvalida},
		{"controle", "a\nb", "", MotivoTagInvalida},
		{"inversão bidi", "a\u202eb", "", MotivoTagInvalida},
		{"largura zero", "a\u200bb", "", MotivoTagInvalida},
		{"acento combinante (forma decomposta)", "produção", "", MotivoTagInvalida},
		{"barra", "a/b", "", MotivoTagInvalida},
		{"aspas", `a"b`, "", MotivoTagInvalida},
		{"percentual", "100%", "", MotivoTagInvalida},
		{"UTF-8 inválido", "a\xffb", "", MotivoTagInvalida},
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
		{"nove", append(slices.Clone(oito), "i"), nil, MotivoTagsDemais},
		// O teto conta o que sobra depois de deduplicar: repetir não é ter mais tags.
		{"nove com uma repetida", append(slices.Clone(oito), "a"), oito, ""},
		{"uma inválida recusa a lista", []string{"ok", "a b"}, nil, MotivoTagInvalida},
		{"uma longa recusa a lista", []string{"ok", strings.Repeat("a", 25)}, nil, MotivoTagLonga},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			saida, motivo := NormalizeTags(c.entrada)
			if motivo != c.motivo {
				t.Fatalf("motivo = %q; queria %q", motivo, c.motivo)
			}
			if c.motivo == "" && (saida == nil || !slices.Equal(saida, c.saida)) {
				t.Errorf("saída = %#v; queria %#v", saida, c.saida)
			}
		})
	}
}
