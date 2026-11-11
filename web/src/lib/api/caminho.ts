// Monta o path de uma rota: cada valor interpolado é codificado como segmento
// de URL, e o literal fica como está. Evita um id com "/", "?" ou "#" mudar a
// rota pedida. A query string não passa por aqui: ela vem do consulta().
export function caminho(literais: TemplateStringsArray, ...valores: string[]): string {
  return literais.reduce((url, literal, i) => {
    const valor = valores[i]
    return url + literal + (valor === undefined ? '' : encodeURIComponent(valor))
  }, '')
}
