import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { emMilissegundos, emPorcento, faixaDoEstado, indicadorDaConsulta, textoDoAviso, ultimoCheck, type ValorDoIndicador } from './estado-do-alvo'

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
    expect(emPorcento(0.99999, 'motivo')).toEqual({ forma: 'valor', valor: '99,99', unidade: '%' })
    expect(emPorcento(1, 'motivo')).toEqual({ forma: 'valor', valor: '100,00', unidade: '%' })
  })

  it('milissegundos inteiros, com separador de milhar', () => {
    expect(emMilissegundos(1234.4, 'motivo')).toEqual({ forma: 'valor', valor: '1.234', unidade: 'ms' })
    expect(emMilissegundos(0, 'motivo')).toEqual({ forma: 'valor', valor: '0', unidade: 'ms' })
  })

  it('sem número, "Sem dados" com o motivo dado', () => {
    expect(emPorcento(null, 'Sem checks em 24 h')).toEqual({ forma: 'sem-dados', motivo: 'Sem checks em 24 h' })
    expect(emMilissegundos(null, 'Sem checks em 24 h')).toEqual({ forma: 'sem-dados', motivo: 'Sem checks em 24 h' })
  })

  it('último check: a hora e há quanto tempo; nunca verificado, sem dados', () => {
    expect(ultimoCheck({ last_checked_at: new Date(2026, 8, 24, 11, 59).toISOString() }, new Date(2026, 8, 24, 12, 0))).toEqual({
      forma: 'valor',
      valor: '11:59',
      unidade: 'há 1 minuto',
    })
    expect(ultimoCheck({ last_checked_at: null }, agora)).toEqual({ forma: 'sem-dados', motivo: 'Nenhum check ainda' })
  })

  it('último check sem o item no resumo: não afirma que nunca houve check', () => {
    expect(ultimoCheck(undefined, agora)).toEqual({ forma: 'sem-dados', motivo: 'Aguardando o resumo' })
  })
})

describe('indicadorDaConsulta', () => {
  const ler = (d: number): ValorDoIndicador => ({ forma: 'valor', valor: String(d), unidade: 'x' })

  it('com dados, lê os dados, mesmo que o poll seguinte tenha falhado', () => {
    expect(indicadorDaConsulta({ data: 7, isLoading: false }, ler)).toEqual({ forma: 'valor', valor: '7', unidade: 'x' })
  })

  it('sem dados e buscando: carregando', () => {
    expect(indicadorDaConsulta({ data: undefined, isLoading: true }, ler)).toEqual({ forma: 'carregando' })
  })

  // isLoading falso sem dados é o erro, e também a consulta pausada sem rede:
  // o esqueleto pulsaria para sempre.
  it('sem dados e sem busca em andamento: indisponível', () => {
    expect(indicadorDaConsulta({ data: undefined, isLoading: false }, ler)).toEqual({ forma: 'indisponivel' })
  })
})

describe('textoDoAviso', () => {
  const instante = new Date(2026, 8, 24, 11, 5).getTime()

  it('resumo falhou sem nada guardado: não se sabe o estado', () => {
    expect(textoDoAviso({ data: undefined, isError: true, dataUpdatedAt: 0 }, false)).toBe('Não foi possível saber o estado deste alvo.')
  })

  it('resumo falhou com dado guardado: diz de quando é o dado', () => {
    expect(textoDoAviso({ data: { items: [] }, isError: true, dataUpdatedAt: instante }, false)).toBe(
      'Não foi possível atualizar; mostrando dados de 11:05.',
    )
  })

  it('só as séries falharam: fala dos indicadores', () => {
    expect(textoDoAviso({ data: { items: [] }, isError: false, dataUpdatedAt: instante }, true)).toBe(
      'Não foi possível atualizar todos os indicadores.',
    )
  })

  it('nada falhou: nenhum aviso', () => {
    expect(textoDoAviso({ data: { items: [] }, isError: false, dataUpdatedAt: instante }, false)).toBeNull()
  })
})
