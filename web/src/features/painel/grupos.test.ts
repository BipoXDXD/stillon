import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { detalheDoProblema, linhaSemDados, ordenarPorGravidade } from './grupos'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const nomes = (itens: { name: string }[]) => itens.map((item) => item.name)
const agora = new Date('2026-09-24T12:00:00Z')

describe('ordenarPorGravidade', () => {
  // Sabotagem: trocar a ordem de GRAVIDADE entre down e degraded.
  it('fora do ar, degradado, no ar, sem dados e pausado, nessa ordem', () => {
    const ordem = ordenarPorGravidade([
      alvo({ id: '1', name: 'Lento', state: 'degraded' }),
      alvo({ id: '2', name: 'Site' }),
      alvo({ id: '3', name: 'Parado', enabled: false }),
      alvo({ id: '4', name: 'Banco', state: 'down' }),
      alvo({ id: '5', name: 'Novo', state: 'unknown' }),
      alvo({ id: '6', name: 'Caiu', state: 'down' }),
    ])
    expect(nomes(ordem)).toEqual(['Banco', 'Caiu', 'Lento', 'Site', 'Novo', 'Parado'])
  })

  it('pausado fica no fim, mesmo com o último estado fora do ar', () => {
    const ordem = ordenarPorGravidade([alvo({ name: 'Parado', state: 'down', enabled: false }), alvo({ name: 'Site' })])
    expect(nomes(ordem)).toEqual(['Site', 'Parado'])
  })

  // Review Focus 8: entre 30 alvos, "Serviço 10" depois de "Serviço 9", e
  // maiúscula não separa.
  it('dentro do mesmo estado, pelo nome: números em ordem e sem diferenciar maiúscula', () => {
    const ordem = ordenarPorGravidade([
      alvo({ id: 'a', name: 'Serviço 10' }),
      alvo({ id: 'b', name: 'serviço 2' }),
      alvo({ id: 'c', name: 'Serviço 1' }),
      alvo({ id: 'd', name: 'api' }),
    ])
    expect(nomes(ordem)).toEqual(['api', 'Serviço 1', 'serviço 2', 'Serviço 10'])
  })

  it('não altera a lista recebida', () => {
    const itens = [alvo({ id: 'a', name: 'B' }), alvo({ id: 'b', name: 'A' })]
    ordenarPorGravidade(itens)
    expect(nomes(itens)).toEqual(['B', 'A'])
  })

  it('lista vazia continua vazia', () => {
    expect(ordenarPorGravidade([])).toEqual([])
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
