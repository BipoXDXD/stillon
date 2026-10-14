import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { agruparPorGravidade, detalheDoProblema, linhaSemDados } from './grupos'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const nomes = (itens: { name: string }[]) => itens.map((item) => item.name)
const agora = new Date('2026-09-24T12:00:00Z')

describe('agruparPorGravidade', () => {
  it('fora do ar antes de degradado; no ar em funcionando; sem dados e pausados no fim', () => {
    const g = agruparPorGravidade([
      alvo({ id: '1', name: 'Lento', state: 'degraded' }),
      alvo({ id: '2', name: 'Site' }),
      alvo({ id: '3', name: 'Parado', enabled: false }),
      alvo({ id: '4', name: 'Banco', state: 'down' }),
      alvo({ id: '5', name: 'Novo', state: 'unknown' }),
      alvo({ id: '6', name: 'Caiu', state: 'down' }),
    ])
    expect(nomes(g.problemas)).toEqual(['Banco', 'Caiu', 'Lento'])
    expect(nomes(g.funcionando)).toEqual(['Site'])
    expect(nomes(g.semDadosOuPausados)).toEqual(['Novo', 'Parado'])
  })

  it('pausado nunca é problema, mesmo com o último estado fora do ar', () => {
    const g = agruparPorGravidade([alvo({ name: 'Parado', state: 'down', enabled: false })])
    expect(g.problemas).toEqual([])
    expect(nomes(g.semDadosOuPausados)).toEqual(['Parado'])
  })

  // Review Focus 8: entre 30 alvos, "Serviço 10" depois de "Serviço 9", e
  // maiúscula não separa.
  it('dentro do grupo, pelo nome: números em ordem e sem diferenciar maiúscula', () => {
    const g = agruparPorGravidade([
      alvo({ id: 'a', name: 'Serviço 10' }),
      alvo({ id: 'b', name: 'serviço 2' }),
      alvo({ id: 'c', name: 'Serviço 1' }),
      alvo({ id: 'd', name: 'api' }),
    ])
    expect(nomes(g.funcionando)).toEqual(['api', 'Serviço 1', 'serviço 2', 'Serviço 10'])
  })

  it('com 32 alvos, nenhum se perde nem se repete', () => {
    const itens = Array.from({ length: 32 }, (_, i) =>
      alvo({ id: `a${i}`, name: `Serviço ${i}`, state: i < 2 ? 'down' : i < 4 ? 'unknown' : 'up' }),
    )
    const g = agruparPorGravidade(itens)
    const todos = [...g.problemas, ...g.funcionando, ...g.semDadosOuPausados].map((item) => item.id)
    expect(new Set(todos).size).toBe(32)
    expect([g.problemas.length, g.funcionando.length, g.semDadosOuPausados.length]).toEqual([2, 28, 2])
  })

  it('lista vazia dá três grupos vazios', () => {
    expect(agruparPorGravidade([])).toEqual({ problemas: [], funcionando: [], semDadosOuPausados: [] })
  })
})

describe('detalheDoProblema', () => {
  it('com incidente aberto: a causa e há quanto tempo', () => {
    const fora = alvo({ state: 'down' })
    expect(detalheDoProblema(fora, inc({ started_at: '2026-09-24T09:29:00Z', cause: 'conn_refused' }), agora)).toBe(
      'Conexão recusada · há 2 h 31 min',
    )
  })

  it('incidente sem causa diz que a causa não foi registrada', () => {
    expect(detalheDoProblema(alvo({ state: 'down' }), inc({ started_at: '2026-09-24T11:59:00Z', cause: '' }), agora)).toBe(
      'Causa não registrada · há 1 min',
    )
  })

  it('degradado sem incidente: a latência do último check', () => {
    expect(detalheDoProblema(alvo({ state: 'degraded', last_latency_ms: 2400 }), undefined, agora)).toBe(
      'Latência de 2.400 ms no último check',
    )
    expect(detalheDoProblema(alvo({ state: 'degraded', last_latency_ms: null }), undefined, agora)).toBe('Lento no último check')
  })

  it('fora do ar sem incidente (ainda não abriu): o último check', () => {
    expect(detalheDoProblema(alvo({ state: 'down', last_checked_at: '2026-09-24T11:57:00Z' }), undefined, agora)).toBe(
      'Último check há 3 minutos',
    )
  })
})

describe('linhaSemDados', () => {
  it.each([
    [{ enabled: false }, 'Pausado'],
    [{ state: 'unknown', last_checked_at: null }, 'Sem dados · nunca verificado'],
    [{ state: 'unknown', last_checked_at: '2026-09-24T11:55:00Z' }, 'Sem dados · último check há 5 minutos'],
  ] as const)('%j → "%s"', (p, texto) => {
    expect(linhaSemDados(alvo(p), agora)).toBe(texto)
  })
})
