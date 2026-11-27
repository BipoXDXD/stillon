/// <reference types="node" />
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { usarApiFalsa } from './ambiente'
import { EMAIL, ERROS_DO_SERVIDOR, PROJETO_PADRAO, SENHA, TOKEN_DO_TURNSTILE, TOTP_OK } from './api-falsa'
import { alvo, canal, heartbeat, incidente, itemDoResumo, pagina } from './fixtures'

// A API falsa é o contrato dos testes de tela: quando ela diverge do servidor,
// a tela passa num estado que não existe. Estes testes fixam o que ela imita.

const TOKEN = 'acesso-do-teste'

function apiLogada() {
  const falsa = usarApiFalsa()
  falsa.estado.acessos.set(TOKEN, falsa.estado.familia)
  return falsa
}

// Os recursos moram no projeto: os caminhos dos testes são os do padrão.
const DO_PROJETO = /^\/(targets|heartbeats|incidents|channels|status-pages)\b/

function pedir(metodo: string, caminhoDoRecurso: string, corpo?: unknown) {
  const caminho = DO_PROJETO.test(caminhoDoRecurso) ? `/projects/${PROJETO_PADRAO}${caminhoDoRecurso}` : caminhoDoRecurso
  return pedirNaRota(metodo, caminho, corpo)
}

function pedirNaRota(metodo: string, caminho: string, corpo?: unknown) {
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

// tenancy.Service.List, ParseProjectName e ProjectLimit no servidor.
describe('projetos (projecthandlers do servidor)', () => {
  // Sabotagem: a lista na ordem de inserção → proj-9 vem primeiro.
  it('a lista traz o padrão primeiro, depois por id', async () => {
    const falsa = apiLogada()
    const nove = falsa.criarProjeto('Nove')
    nove.projeto.id = 'proj-9'
    falsa.estado.projetos = [nove, ...falsa.estado.projetos.filter((p) => p !== nove)]
    falsa.criarProjeto('Três')
    const resposta = await pedir('GET', '/projects')
    const { items } = (await resposta.json()) as { items: { id: string }[] }
    expect(items.map((p) => p.id)).toEqual([PROJETO_PADRAO, 'proj-3', 'proj-9'])
  })

  it.each([
    ['vazio', '  ', 'required'],
    ['101 runas', 'ç'.repeat(101), 'too_long'],
    ['espaço de largura zero', 'a​b', 'invalid'],
  ])('nome %s é 400 no campo name', async (_, name, reason) => {
    apiLogada()
    const resposta = await pedir('POST', '/projects', { name })
    expect(resposta.status).toBe(400)
    expect(await resposta.json()).toMatchObject({ error: { code: 'validation_failed', fields: [{ field: 'name', reason }] } })
  })

  // Sabotagem: contar todos os projetos da lista, não só os do titular → o
  // projeto em que ele é só membro esgota a cota.
  it('o teto conta os projetos de que o usuário é titular', async () => {
    const falsa = apiLogada()
    falsa.estado.limiteDeProjetos = 2
    falsa.criarProjeto('Alheio').projeto.role = 'member'
    expect((await pedir('POST', '/projects', { name: 'Segundo' })).status).toBe(201)
    const resposta = await pedir('POST', '/projects', { name: 'Terceiro' })
    expect(resposta.status).toBe(409)
    expect(await resposta.json()).toMatchObject({ error: { code: 'project_limit_reached' } })
  })
})

// Como optional.go e recusouNulo no servidor: em campo obrigatório, null é 400,
// antes de procurar o recurso e sem gravar nada.
describe('PATCH com null em campo obrigatório', () => {
  const campos = (...nomes: string[]) => nomes.map((field) => ({ field, reason: 'required' }))
  const recursos = [
    {
      caminho: '/targets/x1',
      colecao: 'alvos' as const,
      criar: () => alvo({ id: 'x1', name: 'antes', tags: ['t'] }),
      obrigatorios: ['name', 'url', 'method', 'interval_sec', 'timeout_ms', 'expected_status', 'enabled', 'tls_warn'],
      junto: { tags: [] },
    },
    {
      caminho: '/heartbeats/x1',
      colecao: 'heartbeats' as const,
      criar: () => heartbeat({ id: 'x1', name: 'antes', tags: ['t'] }),
      obrigatorios: ['name', 'schedule', 'grace_sec', 'enabled'],
      junto: { tags: [] },
    },
    {
      caminho: '/status-pages/x1',
      colecao: 'paginas' as const,
      criar: () => pagina({ id: 'x1', slug: 'antes', published: true }),
      obrigatorios: ['slug', 'title', 'published', 'items'],
      junto: { title: 'Depois' },
    },
  ]

  it.each(recursos.flatMap((r) => r.obrigatorios.map((campo) => [r.colecao, campo, r] as const)))(
    '%s: %s responde 400 e não grava nada',
    async (colecao, campo, r) => {
      const falsa = apiLogada()
      falsa.estado[colecao] = [r.criar() as never]
      const antes = structuredClone(falsa.estado[colecao])
      const resposta = await pedir('PATCH', r.caminho, { ...r.junto, [campo]: null })
      expect(resposta.status).toBe(400)
      expect(await resposta.json()).toMatchObject({ error: { code: 'validation_failed', fields: campos(campo) } })
      expect(falsa.estado[colecao]).toEqual(antes)
    },
  )

  it.each(recursos.map((r) => [r.colecao, r] as const))('%s: lista todos os null, na ordem do servidor', async (colecao, r) => {
    const falsa = apiLogada()
    falsa.estado[colecao] = [r.criar() as never]
    const corpo = Object.fromEntries(r.obrigatorios.map((c) => [c, null]))
    const resposta = await pedir('PATCH', r.caminho, corpo)
    expect(await resposta.json()).toMatchObject({ error: { fields: campos(...r.obrigatorios) } })
  })

  it.each(recursos.map((r) => [r.colecao, r] as const))('%s: recusa antes de procurar o recurso', async (_colecao, r) => {
    apiLogada()
    const resposta = await pedir('PATCH', r.caminho, { [r.obrigatorios[0] as string]: null })
    expect(resposta.status).toBe(400)
  })

  // Sabotagem: pôr tags, description ou headers na lista de obrigatórios.
  it.each([
    { caminho: '/targets/x1', colecao: 'alvos' as const, criar: () => alvo({ id: 'x1' }), campo: 'tags' },
    { caminho: '/targets/x1', colecao: 'alvos' as const, criar: () => alvo({ id: 'x1' }), campo: 'headers' },
    { caminho: '/targets/x1', colecao: 'alvos' as const, criar: () => alvo({ id: 'x1' }), campo: 'degraded_threshold_ms' },
    { caminho: '/heartbeats/x1', colecao: 'heartbeats' as const, criar: () => heartbeat({ id: 'x1' }), campo: 'tags' },
    { caminho: '/heartbeats/x1', colecao: 'heartbeats' as const, criar: () => heartbeat({ id: 'x1' }), campo: 'description' },
  ])('$colecao: null em $campo continua valendo', async ({ caminho, colecao, criar, campo }) => {
    const falsa = apiLogada()
    falsa.estado[colecao] = [criar() as never]
    expect((await pedir('PATCH', caminho, { [campo]: null })).status).toBe(200)
  })
})

// O certificado do alvo (Tarefa 6 do PR 6): tls_warn nasce ligado, tls nasce
// null, e outra origem apaga a leitura do anterior, como mudouDeDestino.
describe('certificado do alvo (targethandlers do servidor)', () => {
  const leitura = { not_after: '2026-12-01T00:00:00Z', observed_at: '2026-11-10T00:00:00Z' }
  const novo = { name: 'n', url: 'https://exemplo.com/', method: 'GET', interval_sec: 60, timeout_ms: 1000, expected_status: 200 }

  it('POST sem tls_warn nasce ligado e sem leitura', async () => {
    apiLogada()
    expect(await (await pedir('POST', '/targets', novo)).json()).toMatchObject({ tls_warn: true, tls: null })
  })

  it('POST com tls_warn false grava false', async () => {
    apiLogada()
    expect(await (await pedir('POST', '/targets', { ...novo, tls_warn: false })).json()).toMatchObject({ tls_warn: false })
  })

  it('tls é só de saída: no POST é campo desconhecido', async () => {
    apiLogada()
    expect((await pedir('POST', '/targets', { ...novo, tls: leitura })).status).toBe(400)
  })

  it.each([
    { url: 'https://api.exemplo.com/outro', tls: leitura },
    { url: 'https://outro.exemplo.com/health', tls: null },
    { url: 'http://api.exemplo.com/health', tls: null },
  ])('PATCH para $url deixa tls $tls', async ({ url, tls }) => {
    const falsa = apiLogada()
    falsa.estado.alvos = [alvo({ id: 'x1', tls: leitura })]
    expect(await (await pedir('PATCH', '/targets/x1', { url })).json()).toMatchObject({ tls })
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
    falsa.estado.forcar.set('GET /projects/:projectId/targets', { status: 503, code: 'unavailable' })
    // @ts-expect-error o servidor dá unavailable com 503, nunca com 502
    falsa.estado.forcar.set('GET /projects/:projectId/targets', { status: 502, code: 'unavailable' })
    // @ts-expect-error o código é internal_error
    falsa.estado.forcar.set('GET /projects/:projectId/targets', { status: 500, code: 'internal' })
    expect(falsa.estado.forcar.size).toBe(1)
  })
})

// As rotas públicas do cadastro (contrato HTTP do PR 3): sem Bearer.
describe('o cadastro público', () => {
  function pedirPublico(caminho: string, corpo: unknown) {
    return pedirPublicoComJson(caminho, JSON.stringify(corpo))
  }

  function pedirPublicoComJson(caminho: string, body: string) {
    return fetch(new URL(`/api/v1${caminho}`, location.origin), {
      method: 'POST',
      headers: { 'X-Requested-With': 'stillon', 'Content-Type': 'application/json' },
      body,
    })
  }

  const cadastro = (email: string) => ({ email, password: SENHA, turnstile_token: TOKEN_DO_TURNSTILE })

  it.each([
    ['/auth/signup', cadastro('nova@exemplo.com')],
    ['/auth/password/forgot', { email: EMAIL, turnstile_token: TOKEN_DO_TURNSTILE }],
  ] as const)('desligado, %s devolve 404 antes de validar os campos do corpo', async (caminho, corpo) => {
    const falsa = usarApiFalsa()
    falsa.estado.cadastro.habilitado = false

    const resposta = await pedirPublico(caminho, { ...corpo, campo_extra: true })

    expect(resposta.status).toBe(404)
    expect(await resposta.json()).toMatchObject({ error: { code: 'not_found' } })
    expect([...falsa.estado.cadastro.contas]).toEqual([EMAIL])
    expect(falsa.estado.cadastro.tokens.size).toBe(0)
  })

  it.each(['/auth/signup', '/auth/password/forgot'])('%s só valida JSON malformado quando habilitado', async (caminho) => {
    const falsa = usarApiFalsa()
    falsa.estado.cadastro.habilitado = false

    const desligado = await pedirPublicoComJson(caminho, '{')
    expect(desligado.status).toBe(404)
    expect(await desligado.json()).toMatchObject({ error: { code: 'not_found' } })

    falsa.estado.cadastro.habilitado = true
    const ligado = await pedirPublicoComJson(caminho, '{')
    expect(ligado.status).toBe(400)
    expect(await ligado.json()).toMatchObject({ error: { code: 'validation_failed' } })
  })

  it('signup com campo a mais dá 400', async () => {
    const falsa = usarApiFalsa()
    const r = await pedirPublico('/auth/signup', { ...cadastro('nova@exemplo.com'), name: 'Nova' })
    expect(r.status).toBe(400)
    expect(await r.json()).toMatchObject({ error: { code: 'validation_failed' } })
    expect(falsa.estado.cadastro.contas.has('nova@exemplo.com')).toBe(false)
  })

  it('signup responde 202 igual com e sem conta', async () => {
    const falsa = usarApiFalsa()
    const comConta = await pedirPublico('/auth/signup', cadastro(EMAIL))
    const semConta = await pedirPublico('/auth/signup', cadastro('nova@exemplo.com'))

    expect([comConta.status, semConta.status]).toEqual([202, 202])
    expect(await comConta.text()).toBe('')
    expect(await semConta.text()).toBe('')
    expect([...comConta.headers]).toEqual([...semConta.headers])
    // Só a conta nova ganha um token de confirmação.
    expect(falsa.estado.cadastro.contas.has('nova@exemplo.com')).toBe(true)
    expect([...falsa.estado.cadastro.tokens.values()]).toEqual(['confirm_email'])
  })

  it('confirm consome o token uma vez', async () => {
    const falsa = usarApiFalsa()
    falsa.estado.emailConfirmado = false
    falsa.estado.cadastro.tokens.set('token-1', 'confirm_email')

    const primeira = await pedirPublico('/auth/email/confirm', { token: 'token-1' })
    const segunda = await pedirPublico('/auth/email/confirm', { token: 'token-1' })

    expect(primeira.status).toBe(204)
    expect(falsa.estado.emailConfirmado).toBe(true)
    expect(segunda.status).toBe(400)
    expect(await segunda.json()).toMatchObject({ error: { code: 'token_invalid' } })
  })
})

describe('trava de e-mail antes da criação', () => {
  const projetoSecundario = 'proj-secundario'
  const criarRecursos = [
    ['/targets', { name: 'Início', url: 'https://exemplo.com/', method: 'GET', interval_sec: 60, timeout_ms: 3000, expected_status: 200 }],
    ['/heartbeats', { name: 'Rotina', schedule: { kind: 'period', period_sec: 60 }, grace_sec: 60 }],
    ['/heartbeats/h1/clone', undefined],
    ['/channels', { kind: 'email', email: 'avisos@exemplo.com' }],
    ['/status-pages', { slug: 'indicadores', title: 'Indicadores', published: false, items: [] }],
  ] as const

  const rotas = [
    ...criarRecursos,
    ...criarRecursos.map(([caminho, corpo]) => [`/projects/${projetoSecundario}${caminho}`, corpo] as const),
    ['/projects', { name: 'Novo projeto' }],
    ['/auth/totp/enroll', { password: SENHA }],
  ] as const

  it.each(rotas)('a conta não confirmada recebe 403 em POST %s sem alterar o estado', async (caminho, corpo) => {
    const falsa = apiLogada()
    falsa.estado.emailConfirmado = false
    falsa.estado.heartbeats = [heartbeat({ id: 'h1' })]
    const outro = falsa.criarProjeto('Secundário')
    outro.projeto.id = projetoSecundario
    outro.recursos.heartbeats = [heartbeat({ id: 'h1' })]

    const recursos = () =>
      [falsa.estado, outro.recursos].map(({ alvos, canais, heartbeats, paginas }) => ({ alvos, canais, heartbeats, paginas }))
    const antes = structuredClone(recursos())
    const resposta = await pedirNaRota('POST', caminho, corpo)

    expect(resposta.status).toBe(403)
    expect(await resposta.json()).toMatchObject({ error: { code: 'email_not_verified' } })
    expect(recursos()).toEqual(antes)
    expect(falsa.estado.projetos.map((p) => p.projeto.id)).toEqual([PROJETO_PADRAO, projetoSecundario])
    expect(falsa.estado.totpPendente).toBe(false)
  })

  it.each([
    ['sem acesso válido', false, false, '/projects/ausente/targets', 401, 'unauthorized'],
    ['com sessão revogada', true, true, '/projects/ausente/targets', 401, 'unauthorized'],
    ['sem pertença ao projeto', true, false, '/projects/ausente/targets', 404, 'not_found'],
    ['com e-mail não confirmado', true, false, '/projects', 403, 'email_not_verified'],
  ] as const)('respeita a precedência das guardas %s', async (_caso, autenticada, revogada, caminho, status, code) => {
    const falsa = autenticada ? apiLogada() : usarApiFalsa()
    falsa.estado.familiaRevogada = revogada
    falsa.estado.emailConfirmado = false

    const resposta = await pedirNaRota('POST', caminho, { campo_extra: true })

    expect(resposta.status).toBe(status)
    expect(await resposta.json()).toMatchObject({ error: { code } })
  })

  it.each([
    ['legadas', ''],
    ['de projeto', `/projects/${PROJETO_PADRAO}`],
  ])('ler, editar, apagar e registrar sinal continuam disponíveis nas rotas %s', async (_familia, prefixo) => {
    const falsa = apiLogada()
    falsa.estado.emailConfirmado = false
    falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Rotina' })]

    const leitura = await pedirNaRota('GET', `${prefixo}/heartbeats/h1`)
    expect(leitura.status).toBe(200)
    expect(await leitura.json()).toMatchObject({ name: 'Rotina' })

    const edicao = await pedirNaRota('PATCH', `${prefixo}/heartbeats/h1`, { name: 'Rotina noturna' })
    expect(edicao.status).toBe(200)
    expect(await edicao.json()).toMatchObject({ name: 'Rotina noturna' })

    const sinal = await pedirNaRota('POST', `${prefixo}/heartbeats/h1/ping-now`)
    expect(sinal.status).toBe(200)
    expect(await sinal.json()).toMatchObject({ state: 'up' })

    expect((await pedirNaRota('DELETE', `${prefixo}/heartbeats/h1`)).status).toBe(204)
    expect(falsa.estado.heartbeats).toEqual([])
  })

  it('a confirmação libera a criação nas duas famílias de rotas com a mesma sessão', async () => {
    const falsa = apiLogada()
    falsa.estado.emailConfirmado = false
    falsa.estado.cadastro.tokens.set('token-1', 'confirm_email')
    const caminho = '/channels'
    const corpo = { kind: 'email', email: 'avisos@exemplo.com' }

    expect((await pedirNaRota('POST', caminho, corpo)).status).toBe(403)
    expect((await pedirNaRota('POST', '/auth/email/confirm', { token: 'token-1' })).status).toBe(204)
    expect((await pedirNaRota('POST', caminho, corpo)).status).toBe(201)
    expect((await pedirNaRota('POST', `/projects/${PROJETO_PADRAO}${caminho}`, corpo)).status).toBe(201)
    expect(falsa.estado.canais).toHaveLength(2)
  })

  it.each([
    ['pendente', false, ['confirm_email']],
    ['confirmado', true, []],
  ] as const)('reenviar com e-mail %s responde 202 sem corpo', async (_caso, confirmado, novosTokens) => {
    const falsa = apiLogada()
    falsa.estado.emailConfirmado = confirmado

    const resposta = await pedirNaRota('POST', '/auth/email/resend')

    expect(resposta.status).toBe(202)
    expect(await resposta.text()).toBe('')
    expect([...falsa.estado.cadastro.tokens.values()]).toEqual(novosTokens)
  })
})
