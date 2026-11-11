import { describe, expect, it } from 'vitest'

// Chave escrita à mão não dá erro quando erra: a invalidação só deixa de
// casar, e a tela fica velha. Todas vêm de `chaves` (consultas.ts).
const LITERAL = /(queryKey:|setQueryData\(|getQueryData\(|removeQueries\(|invalidateQueries\()\s*\[/

function achados(arquivo: string, texto: string): string[] {
  return texto
    .split('\n')
    .map((linha, i) => (LITERAL.test(linha) ? `${arquivo}:${i + 1}: ${linha.trim()}` : null))
    .filter((a): a is string => a !== null)
}

describe('guarda de queryKey literal', () => {
  it('acusa array escrito à mão', () => {
    expect(achados('x.ts', "qc.invalidateQueries({ queryKey: ['x'] })")).toHaveLength(1)
    expect(achados('x.ts', "qc.setQueryData(['x'], 1)")).toHaveLength(1)
    expect(achados('x.ts', "qc.invalidateQueries([ 'x' ])")).toHaveLength(1)
  })

  it('aceita chave que vem da fábrica ou de uma variável', () => {
    expect(achados('x.ts', 'useQuery({ queryKey: chaves.me, queryFn })')).toEqual([])
    expect(achados('x.ts', 'qc.removeQueries({ queryKey: chaves.alvo(id) })')).toEqual([])
    expect(achados('x.ts', 'qc.setQueryData(chave, 1)')).toEqual([])
  })

  // As chaves moram em consultas.ts (e é lá que se compõem, com spread).
  it('nenhum código de produção escreve queryKey literal', () => {
    const fontes = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })
    const producao = Object.entries(fontes).filter(
      ([caminho]) => !/\.test\.tsx?$/.test(caminho) && !caminho.startsWith('/src/test/') && caminho !== '/src/lib/api/consultas.ts',
    )
    expect(producao.length).toBeGreaterThan(50) // o glob achou o código; caminho errado não passa em branco
    expect(producao.flatMap(([caminho, texto]) => achados(caminho, texto))).toEqual([])
  })
})
