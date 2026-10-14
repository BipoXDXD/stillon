// Espelho de internal/rotulo/rotulo.go: decide se um texto curto mostrado a
// pessoas (nome de alvo, título e nomes da status page) tem caractere que o
// deixaria mentir na tela. O servidor é a fonte da verdade; o espelho só
// antecipa o erro no campo certo, e por isso as duas regras andam juntas.

export const MENSAGEM_INVISIVEL = 'Sem caracteres invisíveis ou de controle (direção do texto, largura zero, hífen suave).'

const ZWJ = '‍'
const ZWNJ = '‌'
const CONTROLE = /\p{Cc}/u
const FORMATACAO = /\p{Cf}/u
// Graphic do Go (L, M, N, P, S, Zs) sem os espaços. Todo Zs é espaço, então
// sobram L, M, N, P e S; as marcas combinantes (M) contam como visíveis porque
// o seletor de variação de emoji (U+FE0F) e o virama do devanágari vêm logo
// antes do ZWJ.
const VISIVEL = /[\p{L}\p{M}\p{N}\p{P}\p{S}]/u

// temInvisivel diz se s tem caractere de controle (Cc) ou de formatação (Cf).
// ZWJ e ZWNJ passam só entre dois caracteres visíveis, que é onde compõem
// emoji, ligaduras e a escrita persa; no começo, no fim ou ao lado de espaço
// ou de outro invisível, só esconderiam diferença entre dois nomes.
export function temInvisivel(s: string): boolean {
  const pontos = [...s]
  return pontos.some((c, i) => {
    if (CONTROLE.test(c)) return true
    if (!FORMATACAO.test(c)) return false
    return !((c === ZWJ || c === ZWNJ) && entreVisiveis(pontos, i))
  })
}

function entreVisiveis(pontos: string[], i: number): boolean {
  const antes = pontos[i - 1]
  const depois = pontos[i + 1]
  return antes !== undefined && depois !== undefined && VISIVEL.test(antes) && VISIVEL.test(depois)
}
