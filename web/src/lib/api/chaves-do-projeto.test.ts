import { describe, expect, it } from 'vitest'
import { chaves } from './consultas'

type ChavesDoProjeto = ReturnType<typeof chaves.doProjeto>

// Os argumentos de exemplo de cada chave que é função. Uma chave nova sem
// exemplo aqui falha o primeiro teste, em vez de escapar da conferência.
const EXEMPLOS: Partial<Record<keyof ChavesDoProjeto, unknown[]>> = {
  previaDoCron: ['0 3 * * *', 'America/Sao_Paulo'],
  resumoFiltrado: [{ tags: ['loja'], estados: ['down'] }],
  alvo: ['alvo-1'],
  serie: ['alvo-1', '24h'],
  incidentes: ['open', 'alvo-1', 20],
  heartbeat: ['hb-1'],
  eventosDoHeartbeat: ['hb-1'],
  incidentesDoHeartbeat: ['hb-1'],
}

// Todas as chaves do projeto, já como arrays (as funções chamadas com o
// exemplo delas).
function todasAsChaves(id: string): [string, readonly unknown[]][] {
  return Object.entries(chaves.doProjeto(id)).map(([nome, chave]) => {
    if (typeof chave !== 'function') return [nome, chave as readonly unknown[]]
    const exemplo = EXEMPLOS[nome as keyof ChavesDoProjeto]
    if (!exemplo) throw new Error(`chaves.doProjeto.${nome} sem argumentos de exemplo no teste`)
    return [nome, (chave as (...a: unknown[]) => readonly unknown[])(...exemplo)]
  })
}

const ehPrefixo = (prefixo: readonly unknown[], chave: readonly unknown[]) =>
  prefixo.length <= chave.length && prefixo.every((parte, i) => Object.is(parte, chave[i]))

// O cache de um projeto não pode servir outro: trocar de projeto mostraria
// os alvos do anterior. Sabotagem: uma chave de chaves.doProjeto sem o
// prefixo ['projeto', id] → este teste falha com o nome dela.
describe('chaves.doProjeto', () => {
  it('toda chave do projeto começa por [projeto, id]', () => {
    const chavesDeA = todasAsChaves('A')
    expect(chavesDeA.length).toBeGreaterThan(10) // a fábrica devolveu as chaves de recurso
    const semPrefixo = chavesDeA.filter(([, chave]) => !ehPrefixo(['projeto', 'A'], chave)).map(([nome]) => nome)
    expect(semPrefixo).toEqual([])
  })

  it('nenhuma chave de A é prefixo de uma chave de B', () => {
    const deB = todasAsChaves('B').map(([, chave]) => chave)
    const invasoras = todasAsChaves('A')
      .filter(([, chave]) => deB.some((outra) => ehPrefixo(chave, outra)))
      .map(([nome]) => nome)
    expect(invasoras).toEqual([])
  })

  it('a lista de projetos fica fora de qualquer projeto', () => {
    expect(chaves.projetos).toEqual(['projects'])
    expect(todasAsChaves('A').some(([, chave]) => ehPrefixo(chaves.projetos, chave))).toBe(false)
  })
})
