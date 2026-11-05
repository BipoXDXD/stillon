import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { linhaDoAlvo, medidaDaLinha, situacaoNaLinhaDoNome, type LinhaDeMonitor } from './linhas'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const agora = new Date('2026-09-24T12:00:00Z')

describe('linhaDoAlvo', () => {
  // Sabotagem: devolver href /targets sem o id, ou a chave sem o tipo → falha.
  it('monta o modelo que a tabela desenha, sem expor o item da API', () => {
    const linha = linhaDoAlvo(alvo({ id: 'a1', name: 'API', last_latency_ms: 123, uptime_24h: 0.5 }), undefined, agora)
    expect(linha).toMatchObject({
      tipo: 'http',
      chave: 'http:a1',
      nome: 'API',
      href: '/targets/a1',
      estado: 'up',
      latenciaMs: 123,
      uptime24h: 0.5,
      incidenteHref: null,
    })
  })

  it('alvo pausado é exibido como pausado, mesmo com o último estado fora do ar', () => {
    expect(linhaDoAlvo(alvo({ state: 'down', enabled: false }), undefined, agora).estado).toBe('pausado')
  })

  // Sabotagem: ignorar o incidente aberto → a situação vira "Último check…".
  it('fora do ar com incidente aberto: a causa, a duração e o caminho do incidente', () => {
    const fora = alvo({ state: 'down', open_incident_id: 'inc-1' })
    const linha = linhaDoAlvo(fora, inc({ started_at: '2026-09-24T09:29:00Z', cause: 'conn_refused' }), agora)
    expect(linha.situacao).toBe('Conexão recusada · há 2 h 31 min')
    expect(linha.incidenteHref).toBe('/incidents?status=open')
  })

  // O incidente pode ainda não ter chegado (a consulta é outra): o link vale
  // pelo open_incident_id do resumo, e a frase cai no último check.
  it('incidente aberto que ainda não chegou: o link existe e a frase não inventa causa', () => {
    const linha = linhaDoAlvo(alvo({ state: 'down', open_incident_id: 'inc-1', last_checked_at: '2026-09-24T11:57:00Z' }), undefined, agora)
    expect(linha.situacao).toBe('Último check há 3 minutos')
    expect(linha.incidenteHref).toBe('/incidents?status=open')
  })

  it.each([
    ['no ar', { last_checked_at: '2026-09-24T11:59:30Z' }, 'Check há 30 segundos'],
    ['no ar sem check', { last_checked_at: null }, 'Nenhum check ainda'],
    ['degradado', { state: 'degraded', last_latency_ms: 2400 }, 'Latência de 2.400 ms no último check'],
    ['sem dados', { state: 'unknown', last_checked_at: null }, 'Sem dados · nunca verificado'],
    ['pausado', { enabled: false }, 'Pausado'],
  ] as const)('situação de %s', (_, p, texto) => {
    expect(linhaDoAlvo(alvo(p), undefined, agora).situacao).toBe(texto)
  })
})

describe('medidaDaLinha', () => {
  const http = (p: Parameters<typeof itemDoResumo>[0]): LinhaDeMonitor => linhaDoAlvo(alvo(p), undefined, agora)

  // Sabotagem: devolver "0 ms" sem medida → o caso null falha.
  it('a medida do monitor HTTP é a latência; sem ela, o traço', () => {
    expect(medidaDaLinha(http({ last_latency_ms: 1234 }))).toBe('1.234 ms')
    expect(medidaDaLinha(http({ last_latency_ms: null }))).toBe('—')
  })
})

describe('situacaoNaLinhaDoNome', () => {
  // No celular a coluna Situação some. O "no ar" fica em uma altura (spec,
  // Mobile); quem precisa de explicação ganha a segunda linha. Sabotagem:
  // devolver sempre a situação → a linha "no ar" passa de 48 px em 400 px.
  it.each([
    [{ state: 'up' }, false],
    [{ state: 'down' }, true],
    [{ state: 'degraded' }, true],
    [{ state: 'unknown' }, true],
    [{ enabled: false }, true],
  ] as const)('%j → %s', (p, mostra) => {
    const linha = linhaDoAlvo(alvo(p), undefined, agora)
    expect(situacaoNaLinhaDoNome(linha)).toBe(mostra ? linha.situacao : null)
  })
})
