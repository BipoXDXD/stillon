import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { emMilissegundos, emPorcento, faixaDoEstado, ultimoCheck } from './estado-do-alvo'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const agora = new Date('2026-09-24T12:00:00Z')

describe('faixaDoEstado', () => {
  it('fora do ar com incidente aberto: há quanto tempo, a causa e o início', () => {
    const f = faixaDoEstado(alvo({ state: 'down' }), inc({ started_at: '2026-09-24T09:29:00Z', cause: 'conn_refused' }), agora)
    expect(f.estado).toBe('down')
    expect(f.titulo).toBe('Fora do ar há 2 h 31 min')
    expect(f.detalhe).toMatch(/^Conexão recusada · desde 24\/09\/2026/)
  })

  // Review Focus 7.
  it('incidente sem causa', () => {
    expect(faixaDoEstado(alvo({ state: 'down' }), inc({ cause: '' }), agora).detalhe).toMatch(/^Causa não registrada · desde /)
  })

  it('sem incidente: o estado e o último check', () => {
    expect(faixaDoEstado(alvo({ last_checked_at: '2026-09-24T11:57:00Z' }), null, agora)).toEqual({
      estado: 'up',
      titulo: 'No ar',
      detalhe: 'Último check há 3 minutos.',
    })
  })

  it('degradado sem incidente: a latência do último check', () => {
    expect(faixaDoEstado(alvo({ state: 'degraded', last_latency_ms: 2400 }), null, agora).detalhe).toBe(
      'Latência de 2.400 ms no último check.',
    )
  })

  // Review Focus 4.
  it('alvo sem nenhum check', () => {
    expect(faixaDoEstado(alvo({ state: 'unknown', last_checked_at: null }), null, agora)).toEqual({
      estado: 'unknown',
      titulo: 'Sem dados',
      detalhe: 'Nenhum check ainda.',
    })
  })

  it('pausado, mesmo com incidente aberto, diz que os checks pararam', () => {
    expect(faixaDoEstado(alvo({ state: 'down', enabled: false }), inc(), agora)).toEqual({
      estado: 'pausado',
      titulo: 'Pausado',
      detalhe: 'Os checks estão parados. Retome para voltar a vigiar.',
    })
  })
})

describe('valores dos indicadores', () => {
  it('porcentagem truncada, com a unidade à parte', () => {
    expect(emPorcento(0.99999)).toEqual({ valor: '99,99', unidade: '%' })
    expect(emPorcento(1)).toEqual({ valor: '100,00', unidade: '%' })
    expect(emPorcento(null)).toEqual({ valor: '—', unidade: '' })
  })

  it('milissegundos inteiros, com separador de milhar', () => {
    expect(emMilissegundos(1234.4)).toEqual({ valor: '1.234', unidade: 'ms' })
    expect(emMilissegundos(0)).toEqual({ valor: '0', unidade: 'ms' })
    expect(emMilissegundos(null)).toEqual({ valor: '—', unidade: '' })
  })

  it('último check: a hora e há quanto tempo; nunca verificado, traço', () => {
    expect(ultimoCheck(new Date(2026, 8, 24, 11, 59).toISOString(), new Date(2026, 8, 24, 12, 0))).toEqual({
      valor: '11:59',
      unidade: 'há 1 minuto',
    })
    expect(ultimoCheck(null, agora)).toEqual({ valor: '—', unidade: 'nunca' })
  })
})
