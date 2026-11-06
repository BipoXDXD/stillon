/// <reference types="node" />
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { usarApiFalsa } from './ambiente'
import { ERROS_DO_SERVIDOR, TOTP_OK } from './api-falsa'
import { alvo, canal, heartbeat, incidente, itemDoResumo, pagina } from './fixtures'

// A API falsa é o contrato dos testes de tela: quando ela diverge do servidor,
// a tela passa num estado que não existe. Estes testes fixam o que ela imita.

const TOKEN = 'acesso-do-teste'

function apiLogada() {
  const falsa = usarApiFalsa()
  falsa.estado.acessos.set(TOKEN, falsa.estado.familia)
  return falsa
}

function pedir(metodo: string, caminho: string, corpo?: unknown) {
  const headers = new Headers({ 'X-Requested-With': 'stillon', Authorization: `Bearer ${TOKEN}` })
  if (corpo === undefined) return fetch(new URL(`/api/v1${caminho}`, location.origin), { method: metodo, headers })
  headers.set('Content-Type', 'application/json')
  return fetch(new URL(`/api/v1${caminho}`, location.origin), { method: metodo, headers, body: JSON.stringify(corpo) })
}

describe('retomar um heartbeat (statusOf do servidor)', () => {
  async function retomar(p: Parameters<typeof heartbeat>[0], ultimoSinal?: 'start' | 'success' | 'fail') {
    const falsa = apiLogada()
    if (ultimoSinal) falsa.estado.ultimosSinais.set('h1', ultimoSinal)
    falsa.estado.heartbeats = [heartbeat({ id: 'h1', enabled: false, state: 'paused', ...p })]
    const resposta = await pedir('PATCH', '/heartbeats/h1', { enabled: true })
    expect(resposta.status).toBe(200)
    return resposta.json() as Promise<ReturnType<typeof heartbeat>>
  }

  const nuncaTerminou = { last_ping_at: null, last_success_at: null }

  it('sem nenhum sinal fica aguardando', async () => {
    expect((await retomar(nuncaTerminou)).state).toBe('waiting')
  })

  it('só com o sinal de início fica aguardando', async () => {
    const instante = '2026-09-24T11:00:00Z'
    const h = await retomar({ ...nuncaTerminou, last_ping_at: instante, last_start_at: instante }, 'start')
    expect(h.state).toBe('waiting')
  })

  it('o início aberto esquecido na retomada não vira execução terminada', async () => {
    const instante = '2026-09-24T11:00:00Z'
    const falsa = apiLogada()
    falsa.estado.ultimosSinais.set('h1', 'start')
    falsa.estado.heartbeats = [
      heartbeat({ id: 'h1', enabled: false, state: 'paused', ...nuncaTerminou, last_ping_at: instante, last_start_at: instante }),
    ]
    await pedir('PATCH', '/heartbeats/h1', { enabled: true })
    await pedir('PATCH', '/heartbeats/h1', { enabled: false })
    const resposta = await pedir('PATCH', '/heartbeats/h1', { enabled: true })
    expect(await resposta.json()).toMatchObject({ state: 'waiting', last_start_at: null })
  })

  it('com um sinal de falha já terminou uma execução', async () => {
    expect((await retomar({ ...nuncaTerminou, last_ping_at: '2026-09-24T11:00:00Z' }, 'fail')).state).toBe('up')
  })

  it('um sucesso antigo vale mesmo com o início por último', async () => {
    expect((await retomar({}, 'start')).state).toBe('up')
  })

  it('o last_signal fica no servidor: não sai na resposta', async () => {
    const h = await retomar({}, 'success')
    expect(h).not.toHaveProperty('last_signal')
  })

  it('com um sucesso fica em dia', async () => {
    expect((await retomar({})).state).toBe('up')
  })

  it('com incidente aberto fica atrasado, mesmo com prazo futuro', async () => {
    expect((await retomar({ open_incident_id: 'inc-1' })).state).toBe('late')
  })

  it('o incidente aberto vale também para quem nunca terminou', async () => {
    expect((await retomar({ ...nuncaTerminou, open_incident_id: 'inc-1' })).state).toBe('late')
  })

  it('esquece o início aberto e empurra o prazo para o futuro', async () => {
    const h = await retomar({ last_start_at: '2026-09-24T11:00:00Z', deadline_at: '2026-09-01T00:00:00Z' })
    expect(h.last_start_at).toBeNull()
    expect(new Date(h.deadline_at).getTime()).toBeGreaterThan(new Date('2026-09-24T12:00:00Z').getTime())
  })

  it('pausar mostra pausado', async () => {
    const falsa = apiLogada()
    falsa.estado.heartbeats = [heartbeat({ id: 'h1' })]
    const resposta = await pedir('PATCH', '/heartbeats/h1', { enabled: false })
    expect(await resposta.json()).toMatchObject({ enabled: false, state: 'paused' })
  })

  it('enabled: true em quem já está ativo não é retomada', async () => {
    const falsa = apiLogada()
    falsa.estado.heartbeats = [heartbeat({ id: 'h1', state: 'failing' })]
    const resposta = await pedir('PATCH', '/heartbeats/h1', { enabled: true })
    expect(await resposta.json()).toMatchObject({ state: 'failing' })
  })

  it('editar o nome de quem está pausado não retoma', async () => {
    const falsa = apiLogada()
    falsa.estado.heartbeats = [heartbeat({ id: 'h1', enabled: false, state: 'paused' })]
    const resposta = await pedir('PATCH', '/heartbeats/h1', { name: 'Novo' })
    expect(await resposta.json()).toMatchObject({ name: 'Novo', state: 'paused' })
  })
})

describe('o estado depois de um PATCH (statusOf do servidor)', () => {
  const PRAZO_PASSADO = '2026-09-24T11:00:00Z'

  async function renomear(p: Parameters<typeof heartbeat>[0], corpo: object = { name: 'Novo' }) {
    const falsa = apiLogada()
    falsa.estado.heartbeats = [heartbeat({ id: 'h1', ...p })]
    const resposta = await pedir('PATCH', '/heartbeats/h1', corpo)
    expect(resposta.status).toBe(200)
    return resposta.json() as Promise<ReturnType<typeof heartbeat>>
  }

  it('renomear não apaga a falha', async () => {
    expect((await renomear({ state: 'failing', deadline_at: PRAZO_PASSADO })).state).toBe('failing')
  })

  it('prazo vencido é atraso, seja qual for o estado guardado', async () => {
    expect((await renomear({ state: 'up', deadline_at: PRAZO_PASSADO })).state).toBe('late')
  })

  it('o prazo que vence exatamente agora já é atraso (!deadline.After(now))', async () => {
    expect((await renomear({ state: 'up', deadline_at: '2026-09-24T12:00:00Z' })).state).toBe('late')
  })

  it('o incidente aberto é atraso mesmo com prazo futuro', async () => {
    expect((await renomear({ state: 'up', open_incident_id: 'inc-1' })).state).toBe('late')
  })

  it('quem nunca terminou segue aguardando', async () => {
    const h = await renomear({ state: 'waiting', last_ping_at: null, last_success_at: null })
    expect(h.state).toBe('waiting')
  })

  it('editar o agendamento de quem atrasou move o prazo e tira o atraso', async () => {
    const h = await renomear({ state: 'late', deadline_at: PRAZO_PASSADO }, { schedule: { kind: 'period', period_sec: 7200 } })
    expect(h.state).toBe('up')
    expect(new Date(h.deadline_at).getTime()).toBeGreaterThan(new Date('2026-09-24T12:00:00Z').getTime())
  })

  it('editar a tolerância também move o prazo', async () => {
    expect((await renomear({ state: 'late', deadline_at: PRAZO_PASSADO }, { grace_sec: 60 })).state).toBe('up')
  })

  it('reenviar o mesmo agendamento não move o prazo', async () => {
    const h = await renomear(
      { state: 'late', deadline_at: PRAZO_PASSADO },
      { schedule: { kind: 'period', period_sec: 86400 }, grace_sec: 900 },
    )
    expect(h).toMatchObject({ state: 'late', deadline_at: PRAZO_PASSADO })
  })

  it('o mesmo agendamento com as chaves em outra ordem não move o prazo', async () => {
    const h = await renomear(
      { state: 'late', deadline_at: PRAZO_PASSADO, schedule: { kind: 'cron', expr: '0 6 * * *', timezone: 'UTC' } },
      { schedule: { timezone: 'UTC', expr: '0 6 * * *', kind: 'cron' } },
    )
    expect(h).toMatchObject({ state: 'late', deadline_at: PRAZO_PASSADO })
  })

  const CRON = { kind: 'cron', expr: '0 6 * * *', timezone: 'UTC' } as const

  it.each([
    ['period_sec', { kind: 'period', period_sec: 86400 }, { kind: 'period', period_sec: 7200 }],
    ['expr', CRON, { ...CRON, expr: '0 7 * * *' }],
    ['timezone', CRON, { ...CRON, timezone: 'America/Sao_Paulo' }],
    ['kind', { kind: 'period', period_sec: 86400 }, { kind: 'cron', period_sec: 86400 }],
  ] as const)('mudar só %s move o prazo', async (_campo, schedule, novo) => {
    const h = await renomear({ state: 'late', deadline_at: PRAZO_PASSADO, schedule }, { schedule: novo })
    expect(h.state).toBe('up')
    expect(h.deadline_at).not.toBe(PRAZO_PASSADO)
  })

  it('a tarefa recém-criada fica aguardando depois de editada', async () => {
    apiLogada()
    const criada = await pedir('POST', '/heartbeats', { name: 'Nova', schedule: { kind: 'period', period_sec: 3600 } })
    const { id } = (await criada.json()) as { id: string }
    const resposta = await pedir('PATCH', `/heartbeats/${id}`, { name: 'Nova editada' })
    expect(await resposta.json()).toMatchObject({ state: 'waiting' })
  })
})

describe.each([
  ['/targets', 'alvos' as const, (id: string) => alvo({ id })],
  ['/heartbeats', 'heartbeats' as const, (id: string) => heartbeat({ id })],
])('paginação de %s (o servidor dá next_cursor com a página cheia)', (rota, colecao, criar) => {
  async function listar(consulta: string) {
    const resposta = await pedir('GET', `${rota}${consulta}`)
    expect(resposta.status).toBe(200)
    return (await resposta.json()) as { items: { id: string }[]; next_cursor?: string }
  }

  function comItens(quantidade: number) {
    const falsa = apiLogada()
    const itens = Array.from({ length: quantidade }, (_, i) => criar(`i-${i + 1}`))
    if (colecao === 'alvos') falsa.estado.alvos = itens as ReturnType<typeof alvo>[]
    else falsa.estado.heartbeats = itens as ReturnType<typeof heartbeat>[]
  }

  it('respeita o limit e aponta o cursor quando a página vem cheia', async () => {
    comItens(3)
    const primeira = await listar('?limit=2')
    expect(primeira.items.map((i) => i.id)).toEqual(['i-1', 'i-2'])
    expect(primeira.next_cursor).toBe('i-2')
  })

  it('a página seguinte começa depois do cursor', async () => {
    comItens(3)
    expect((await listar('?limit=2&cursor=i-2')).items.map((i) => i.id)).toEqual(['i-3'])
  })

  it('página incompleta não traz next_cursor', async () => {
    comItens(1)
    expect(await listar('?limit=2')).not.toHaveProperty('next_cursor')
  })
})

describe('paginação dos incidentes (o servidor dá next_cursor com a página cheia)', () => {
  type Pagina = { items: { id: string }[]; next_cursor?: string }

  function comIncidentes(quantidade: number) {
    apiLogada().estado.incidentes = Array.from({ length: quantidade }, (_, i) => incidente({ id: `inc-${i + 1}` }))
  }

  async function listar(consulta: string) {
    const resposta = await pedir('GET', `/incidents${consulta}`)
    expect(resposta.status).toBe(200)
    return (await resposta.json()) as Pagina
  }

  it('página cheia e nada depois ainda traz next_cursor', async () => {
    comIncidentes(2)
    expect((await listar('?limit=2')).next_cursor).toBe('inc-2')
  })

  it('a página seguinte vem vazia e sem next_cursor', async () => {
    comIncidentes(2)
    expect(await listar('?limit=2&cursor=inc-2')).toEqual({ items: [] })
  })

  it('página incompleta não traz next_cursor', async () => {
    comIncidentes(1)
    expect((await listar('?limit=2')).next_cursor).toBeUndefined()
  })

  it('com mais itens que o limite traz next_cursor', async () => {
    comIncidentes(3)
    const primeira = await listar('?limit=2')
    expect(primeira.items).toHaveLength(2)
    expect(primeira.next_cursor).toBe('inc-2')
  })

  it('pagina depois do filtro de estado', async () => {
    apiLogada().estado.incidentes = [
      incidente({ id: 'inc-1' }),
      incidente({ id: 'inc-2', resolved_at: '2026-09-24T11:30:00Z' }),
    ]
    expect(await listar('?status=open&limit=1')).toMatchObject({ items: [{ id: 'inc-1' }], next_cursor: 'inc-1' })
  })
})

describe('entrada estrita (decodeJSON do servidor)', () => {
  const ROTAS: [string, string, Record<string, unknown>][] = [
    ['POST', '/auth/login', { email: 'a@b.c', password: 'x' }],
    ['POST', '/auth/totp/verify', { challenge: 'x', code: '1' }],
    ['POST', '/auth/password', { current_password: 'x', new_password: 'uma senha bem comprida', totp_code: '' }],
    ['POST', '/auth/totp/enroll', { password: 'x' }],
    ['POST', '/auth/totp/confirm', { code: TOTP_OK }],
    ['POST', '/auth/totp/disable', { password: 'x', code: '1', recovery_code: '' }],
    ['POST', '/auth/totp/rotate', { password: 'x', code: '1', recovery_code: '' }],
    ['POST', '/auth/totp/rotate/confirm', { code: '1', rotation: 'r' }],
    ['POST', '/targets', { name: 'a', url: 'https://a.b', method: 'GET', tags: [], headers: {} }],
    ['PATCH', '/targets/alvo-1', { name: 'a', tags: [], headers: { Chave: 'v' } }],
    ['POST', '/heartbeats', { name: 'a', schedule: { kind: 'period', period_sec: 60 }, grace_sec: 60 }],
    ['PATCH', '/heartbeats/h1', { name: 'a', description: '', enabled: true }],
    ['POST', '/channels', { kind: 'email', email: 'a@b.c' }],
    ['POST', '/status-pages', { slug: 'a', title: 'A', published: false, items: [] }],
    ['PATCH', '/status-pages/pagina-1', { title: 'A', items: [{ target_id: 'alvo-1', display_name: 'A' }] }],
  ]

  function apiComRecursos() {
    const falsa = apiLogada()
    falsa.estado.alvos = [alvo({ id: 'alvo-1' })]
    falsa.estado.heartbeats = [heartbeat({ id: 'h1' })]
    falsa.estado.paginas = [pagina({ id: 'pagina-1' })]
    falsa.estado.totpPendente = true
    return falsa
  }

  it.each(ROTAS)('%s %s recusa campo desconhecido com 400 sem fields', async (metodo, caminho, corpo) => {
    apiComRecursos()
    const resposta = await pedir(metodo, caminho, { ...corpo, extra: 1 })
    expect(resposta.status).toBe(400)
    const { error } = (await resposta.json()) as { error: { code: string; fields?: unknown } }
    expect(error.code).toBe('validation_failed')
    expect(error.fields).toBeUndefined()
  })

  // O controle: sem ele, uma rota que recusasse tudo passaria no caso acima.
  it.each(ROTAS)('%s %s aceita os campos do contrato', async (metodo, caminho, corpo) => {
    apiComRecursos()
    expect((await pedir(metodo, caminho, corpo)).status).not.toBe(400)
  })

  it('recusa campo desconhecido dentro do agendamento', async () => {
    apiComRecursos()
    const corpo = { name: 'a', schedule: { kind: 'period', period_sec: 60, extra: 1 } }
    expect((await pedir('POST', '/heartbeats', corpo)).status).toBe(400)
    expect((await pedir('PATCH', '/heartbeats/h1', corpo)).status).toBe(400)
  })

  it('recusa campo desconhecido dentro dos itens da página de status', async () => {
    apiComRecursos()
    const corpo = { slug: 'a', title: 'A', items: [{ target_id: 'alvo-1', display_name: 'A', extra: 1 }] }
    expect((await pedir('POST', '/status-pages', corpo)).status).toBe(400)
    expect((await pedir('PATCH', '/status-pages/pagina-1', corpo)).status).toBe(400)
  })

  it('os headers do alvo são um mapa livre, não um objeto com campos fixos', async () => {
    apiComRecursos()
    expect((await pedir('PATCH', '/targets/alvo-1', { headers: { 'X-Qualquer': 'v' } })).status).toBe(200)
  })

  it('o campo recusado não altera o recurso', async () => {
    const falsa = apiComRecursos()
    await pedir('PATCH', '/heartbeats/h1', { name: 'Novo', extra: 1 })
    expect(falsa.estado.heartbeats[0]?.name).toBe('Backup noturno')
  })
})

describe('erros que só o servidor sabe dar', () => {
  it('a segunda status page é conflito, e a primeira fica', async () => {
    const falsa = apiLogada()
    falsa.estado.paginas = [pagina({ slug: 'primeira' })]
    const resposta = await pedir('POST', '/status-pages', { slug: 'outra', title: 'Outra', published: false, items: [] })
    expect(resposta.status).toBe(409)
    expect(await resposta.json()).toMatchObject({ error: { code: 'status_page_exists' } })
    expect(falsa.estado.paginas.map((p) => p.slug)).toEqual(['primeira'])
  })

  it.each(['pending', 'expired'] as const)('reenviar verificação de e-mail %s funciona', async (status) => {
    const falsa = apiLogada()
    falsa.estado.canais = [canal({ id: 'c1', kind: 'email', status })]
    expect((await pedir('POST', '/channels/c1/verify/resend')).status).toBe(204)
  })

  it.each([
    ['e-mail já verificado', { kind: 'email', status: 'verified' }],
    ['e-mail verificado que passou a falhar', { kind: 'email', status: 'failing' }],
    ['discord', { kind: 'discord', status: 'pending' }],
  ] as const)('reenviar verificação de %s é conflito', async (_nome, p) => {
    const falsa = apiLogada()
    falsa.estado.canais = [canal({ id: 'c1', ...p })]
    const resposta = await pedir('POST', '/channels/c1/verify/resend')
    expect(resposta.status).toBe(409)
    expect(await resposta.json()).toMatchObject({ error: { code: 'channel_not_pending' } })
  })
})

describe('PATCH com name null (a validação do nome no servidor)', () => {
  it.each([
    ['alvo', '/targets/x1', 'alvos' as const, () => alvo({ id: 'x1', name: 'antes' })],
    ['heartbeat', '/heartbeats/x1', 'heartbeats' as const, () => heartbeat({ id: 'x1', name: 'antes' })],
  ])('%s responde 400 e não grava nada', async (_nome, caminho, colecao, criar) => {
    const falsa = apiLogada()
    falsa.estado[colecao] = [criar() as never]
    const resposta = await pedir('PATCH', caminho, { name: null, enabled: false })
    expect(resposta.status).toBe(400)
    expect(await resposta.json()).toMatchObject({
      error: { code: 'validation_failed', fields: [{ field: 'name', reason: 'required' }] },
    })
    expect(falsa.estado[colecao][0]).toMatchObject({ name: 'antes', enabled: true })
  })
})

describe('filtro do resumo (ParseSummaryFilter do servidor)', () => {
  function apiComResumo() {
    const falsa = apiLogada()
    falsa.estado.resumo = [
      itemDoResumo({ id: 'a1', tags: ['web'], state: 'up' }),
      itemDoResumo({ id: 'a2', tags: ['banco'], state: 'down' }),
    ]
  }

  it('normaliza a tag como o servidor', async () => {
    apiComResumo()
    const corpo = (await (await pedir('GET', '/targets/summary?tag=%20WEB%20')).json()) as { items: { id: string }[] }
    expect(corpo.items.map((i) => i.id)).toEqual(['a1'])
  })

  it.each([
    ['tag com separador', 'tag=a,b', 'tag', 'invalid'],
    ['tag com número que não é dígito', 'tag=a%C2%B2', 'tag', 'invalid'],
    ['tag vazia', 'tag=', 'tag', 'invalid'],
    ['tag no limite + 1', `tag=${'a'.repeat(25)}`, 'tag', 'too_long'],
    ['tags demais', Array.from({ length: 9 }, (_, i) => `tag=t${i}`).join('&'), 'tag', 'out_of_range'],
    ['estado inválido', 'state=late', 'state', 'invalid'],
    ['estados demais', Array.from({ length: 6 }, () => 'state=up').join('&'), 'state', 'out_of_range'],
    ['parâmetro desconhecido', 'cor=azul', 'query', 'invalid'],
  ])('%s é 400', async (_nome, consulta, field, reason) => {
    apiComResumo()
    const resposta = await pedir('GET', `/targets/summary?${consulta}`)
    expect(resposta.status).toBe(400)
    expect(await resposta.json()).toMatchObject({ error: { code: 'validation_failed', fields: [{ field, reason }] } })
  })

  it('tag com dígito de outra escrita passa', async () => {
    apiComResumo()
    expect((await pedir('GET', '/targets/summary?tag=a%D9%A3')).status).toBe(200)
  })

  it('tag no limite passa', async () => {
    apiComResumo()
    expect((await pedir('GET', `/targets/summary?tag=${'a'.repeat(24)}`)).status).toBe(200)
  })
})

describe('tabela de erros (errorTable do servidor)', () => {
  const STATUS: Record<string, number> = {
    StatusBadRequest: 400,
    StatusUnauthorized: 401,
    StatusForbidden: 403,
    StatusNotFound: 404,
    StatusMethodNotAllowed: 405,
    StatusConflict: 409,
    StatusRequestEntityTooLarge: 413,
    StatusTooManyRequests: 429,
    StatusInternalServerError: 500,
    StatusBadGateway: 502,
    StatusServiceUnavailable: 503,
  }

  it('é a de internal/apierr/errors.go', () => {
    const go = readFileSync(path.resolve(import.meta.dirname, '../../../internal/apierr/errors.go'), 'utf8')
    const codigos = new Map([...go.matchAll(/(Code\w+)\s+ErrorCode = "(\w+)"/g)].map((m) => [m[1]!, m[2]!]))
    const tabela = [...go.matchAll(/(Code\w+):\s+\{http\.(Status\w+),/g)].map((m) => [codigos.get(m[1]!), STATUS[m[2]!]])
    expect(tabela.length).toBeGreaterThan(0)
    expect(Object.fromEntries(tabela)).toEqual(ERROS_DO_SERVIDOR)
  })

  it('forcar só aceita o par que o servidor emite', () => {
    const falsa = apiLogada()
    falsa.estado.forcar.set('GET /targets', { status: 503, code: 'unavailable' })
    // @ts-expect-error o servidor dá unavailable com 503, nunca com 502
    falsa.estado.forcar.set('GET /targets', { status: 502, code: 'unavailable' })
    // @ts-expect-error o código é internal_error
    falsa.estado.forcar.set('GET /targets', { status: 500, code: 'internal' })
    expect(falsa.estado.forcar.size).toBe(1)
  })
})
