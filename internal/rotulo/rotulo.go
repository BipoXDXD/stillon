// Package rotulo decide se um texto curto mostrado a pessoas (nome de alvo,
// título e nomes da página de status) pode enganar quem o lê.
package rotulo

import "unicode"

// Os dois caracteres de formatação (Cf) que a escrita legítima precisa: o
// ZWJ (U+200D) monta emoji compostos (profissão, família, "coração em chamas")
// e ligaduras do devanágari; o ZWNJ (U+200C) separa letras no persa e em
// escritas da Índia.
const (
	zwj  = 0x200D
	zwnj = 0x200C
)

// TemInvisivel diz se s tem caractere de controle (Cc) ou de formatação (Cf)
// que deixaria o texto mentir na tela: inversão e isolamento bidi, marcas de
// direção, espaço de largura zero, BOM, hífen suave, etiquetas invisíveis.
//
// ZWJ e ZWNJ passam só entre dois caracteres visíveis, que é onde eles
// compõem algo. No começo, no fim, ao lado de espaço ou de outro invisível,
// não compõem nada e só esconderiam diferença entre dois nomes. Ficam de fora,
// por serem etiquetas (Cf), as bandeiras de subdivisão (Inglaterra, Escócia,
// País de Gales).
func TemInvisivel(s string) bool {
	runas := []rune(s)
	for i, r := range runas {
		if unicode.IsControl(r) {
			return true
		}
		if !unicode.Is(unicode.Cf, r) {
			continue
		}
		if (r == zwj || r == zwnj) && entreVisiveis(runas, i) {
			continue
		}
		return true
	}
	return false
}

func entreVisiveis(runas []rune, i int) bool {
	return i > 0 && i < len(runas)-1 && visivel(runas[i-1]) && visivel(runas[i+1])
}

// visivel inclui as marcas combinantes (Mn): o seletor de variação de emoji
// (U+FE0F) e o virama do devanágari vêm logo antes do ZWJ.
func visivel(r rune) bool {
	return unicode.IsGraphic(r) && !unicode.IsSpace(r)
}
