package rotulo_test

import (
	"testing"

	"github.com/BipoXDXD/stillon/internal/rotulo"
)

func TestTemInvisivel(t *testing.T) {
	casos := []struct {
		nome      string
		s         string
		invisivel bool
	}{
		{"vazio", "", false},
		{"ascii", "API de pagamentos", false},
		{"acento", "Página de preço", false},
		{"emoji simples", "Loja \U0001F6D2", false},
		{"emoji de profissão (ZWJ)", "Equipe \U0001F469\U0000200D\U0001F4BB", false},
		{"emoji com seletor de variação antes do ZWJ", "\U00002764\U0000FE0F\U0000200D\U0001F525", false},
		{"emoji com tom de pele antes do ZWJ", "\U0001F468\U0001F3FD\U0000200D\U0001F680", false},
		{"persa com ZWNJ", "می\U0000200Cخواهم", false},
		{"devanágari com virama antes do ZWJ", "क\U0000094D\U0000200Dष", false},

		{"controle", "a\x00b", true},
		{"quebra de linha", "a\nb", true},
		{"inversão bidi", "a\U0000202Eb", true},
		{"isolamento bidi", "a\U00002066b\U00002069", true},
		{"marca de direção", "a\U0000200Fb", true},
		{"espaço de largura zero", "a\U0000200Bb", true},
		{"BOM no meio", "a\U0000FEFFb", true},
		{"hífen suave", "a\U000000ADb", true},
		{"etiqueta invisível", "a\U000E0041b", true},
		{"ZWJ no começo", "\U0000200Da", true},
		{"ZWJ no fim", "a\U0000200D", true},
		{"ZWJ sozinho", "\U0000200D", true},
		{"ZWJ duplo", "a\U0000200D\U0000200Db", true},
		{"ZWJ antes de espaço", "a\U0000200D b", true},
		{"ZWJ depois de espaço", "a \U0000200Db", true},
		{"ZWNJ antes de inversão bidi", "a\U0000200C\U0000202Eb", true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := rotulo.TemInvisivel(c.s); got != c.invisivel {
				t.Errorf("TemInvisivel(%+q) = %v, queria %v", c.s, got, c.invisivel)
			}
		})
	}
}
