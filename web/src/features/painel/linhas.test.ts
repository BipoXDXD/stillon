import { describe, expect, it } from 'vitest'
import { Heartbeat, Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { heartbeat, incidente, itemDoResumo } from '@/test/fixtures'
import { ordenarPorGravidade } from './grupos'
import { linhaDoAlvo, linhaDoHeartbeat, medidaDaLinha, situacaoNaLinhaDoNome, tipoSemCaso, type LinhaDeMonitor } from './linhas'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const hb = (p: Parameters<typeof heartbeat>[0] = {}) => Heartbeat.parse(heartbeat(p))
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

  // D1 da revisão: um instante à frente do relógio de referência não vira
  // "Check em 3 segundos". Sabotagem: voltar a haQuanto em linhas.ts e grupos.ts.
  it.each([
    ['no ar', { last_checked_at: '2026-09-24T12:00:03Z' }, 'Check agora'],
    ['fora do ar sem incidente', { state: 'down', last_checked_at: '2026-09-24T12:00:03Z' }, 'Último check agora'],
    ['sem dados', { state: 'unknown', last_checked_at: '2026-09-24T12:00:03Z' }, 'Sem dados · último check agora'],
  ] as const)('instante no futuro, %s', (_, p, texto) => {
    expect(linhaDoAlvo(alvo(p), undefined, agora).situacao).toBe(texto)
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

describe('linhaDoHeartbeat', () => {
  // Sabotagem: apontar o href para /targets, ou chavear só pelo id → falha.
  it('monta a linha na mesma forma, sem medida nem histórico', () => {
    expect(linhaDoHeartbeat(hb({ id: 'h1', name: 'Backup', last_ping_at: '2026-09-24T11:58:00Z' }), agora)).toEqual({
      tipo: 'heartbeat',
      chave: 'heartbeat:h1',
      nome: 'Backup',
      href: '/heartbeats/h1',
      estado: 'up',
      textoDoEstado: 'Em dia',
      situacao: 'Sinal há 2 minutos',
      tags: [],
      uptime24h: null,
      historico: [],
      incidenteHref: null,
    })
  })

  it.each([
    ['atrasado', { state: 'late', deadline_at: '2026-09-24T09:29:00Z' }, 'Sem sinal no prazo · há 2 h 31 min'],
    // Editar um atrasado põe o prazo à frente e o incidente fica (spec de
    // heartbeats, §5). Sabotagem: tirar a guarda → "há menos de 1 min".
    ['atrasado com prazo novo', { state: 'late', deadline_at: '2026-09-25T09:00:00Z' }, 'Sem sinal no prazo'],
    ['falhando', { state: 'failing' }, 'A tarefa avisou falha'],
    ['aguardando', { state: 'waiting', last_ping_at: null }, 'Aguardando o primeiro sinal'],
    ['aguardando, com a primeira execução em curso', { state: 'waiting', last_start_at: '2026-09-24T11:58:00Z' }, 'Em execução, começou há 2 minutos'],
    ['em dia, com duração', { state: 'up', last_runtime_ms: 12_000 }, 'Sinal há 2 minutos · levou 12 s'],
    ['em dia, com duração zero', { state: 'up', last_runtime_ms: 0 }, 'Sinal há 2 minutos · levou 0 ms'],
    ['em dia, com execução em curso', { state: 'up', last_start_at: '2026-09-24T11:58:00Z', last_runtime_ms: 12_000 }, 'Em execução, começou há 2 minutos'],
    ['atrasado com início aberto não diz que está em execução', { state: 'late', last_start_at: '2026-09-24T11:58:00Z', deadline_at: '2026-09-24T11:59:00Z' }, 'Sem sinal no prazo · há 1 min'],
    ['pausado', { state: 'paused', enabled: false }, 'Pausado'],
    ['em dia sem sinal', { state: 'up', last_ping_at: null }, 'Nenhum sinal ainda'],
  ] as const)('situação de %s', (_, p, texto) => {
    expect(linhaDoHeartbeat(hb(p), agora).situacao).toBe(texto)
  })

  it('com incidente aberto, o link vai ao detalhe: a tela de incidentes não o traz (D3)', () => {
    expect(linhaDoHeartbeat(hb({ id: 'h1', state: 'late', open_incident_id: 'i1' }), agora).incidenteHref).toBe('/heartbeats/h1')
  })

  // Spec da tela, T3: uma escala de gravidade só. Sabotagem: mapear late para
  // unknown em estado.ts → o atrasado desce para depois do alvo no ar.
  it('entra na ordem de gravidade junto dos alvos', () => {
    const linhas = ordenarPorGravidade([
      linhaDoAlvo(alvo({ id: 'a', name: 'API' }), undefined, agora),
      linhaDoHeartbeat(hb({ id: 'h1', name: 'Backup', state: 'late' }), agora),
      linhaDoAlvo(alvo({ id: 'b', name: 'Caiu', state: 'down' }), undefined, agora),
      linhaDoHeartbeat(hb({ id: 'h2', name: 'Relatório', state: 'waiting' }), agora),
    ])
    expect(linhas.map((l) => l.nome)).toEqual(['Backup', 'Caiu', 'API', 'Relatório'])
  })
})

describe('medidaDaLinha', () => {
  const http = (p: Parameters<typeof itemDoResumo>[0]): LinhaDeMonitor => linhaDoAlvo(alvo(p), undefined, agora)

  // Sabotagem: devolver "0 ms" sem medida → o caso null falha.
  it('a medida do monitor HTTP é a latência; sem ela, o traço', () => {
    expect(medidaDaLinha(http({ last_latency_ms: 1234 }))).toBe('1.234 ms')
    expect(medidaDaLinha(http({ last_latency_ms: null }))).toBe('—')
  })

  it('a linha do heartbeat traz as tags dele', () => {
    expect(linhaDoHeartbeat(hb({ tags: ['prod', 'dados'] }), agora).tags).toEqual(['prod', 'dados'])
  })

  it('o heartbeat não tem latência: o traço, sem inventar medida', () => {
    expect(medidaDaLinha(linhaDoHeartbeat(hb(), agora))).toBe('—')
  })
})

describe('switch exaustivo sobre tipo', () => {
  // M4 da revisão. A prova de compilação é o @ts-expect-error: um tipo ainda
  // não tratado não cabe em never, e por isso um membro novo sem caso em
  // medidaDaLinha não compila.
  // Sabotagem: acrescentar um membro à união sem caso → o tsc -b recusa.
  it('um tipo fora da união falha alto em vez de virar célula vazia', () => {
    const linha = linhaDoAlvo(alvo(), undefined, agora)
    const intrusa = { ...linha, tipo: 'ping' } as unknown as LinhaDeMonitor
    expect(() => medidaDaLinha(intrusa)).toThrow('Tipo de monitor sem caso: ping')
    // @ts-expect-error: o tipo só cabe em never depois de esgotar os casos.
    expect(() => tipoSemCaso(linha.tipo)).toThrow('Tipo de monitor sem caso: http')
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
