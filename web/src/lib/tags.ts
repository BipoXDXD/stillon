// A regra das tags, espelho de internal/targets/tags.go. O servidor é a fonte
// da verdade; aqui só se antecipa o erro. \p{L} não aceita o acento
// combinante, como o unicode.IsLetter do Go: uma palavra tem uma grafia só.
export const MAX_TAGS = 8
export const MAX_CARACTERES_DA_TAG = 24

const TAG = /^[\p{L}\p{Nd}_-]+$/u

export function normalizarTag(cru: string): string {
  return cru.trim().toLowerCase()
}

export function ehTagValida(tag: string): boolean {
  return TAG.test(tag) && [...tag].length <= MAX_CARACTERES_DA_TAG
}

// O campo do formulário: separado por vírgula, normalizado e sem repetição.
export function separarTags(texto: string): string[] {
  const tags = texto.split(',').map(normalizarTag).filter(Boolean)
  return [...new Set(tags)].sort()
}
