import { delay, http, HttpResponse, type HttpHandler } from 'msw'
import type { z } from 'zod'
import type {
  Alvo,
  Canal,
  Check,
  EventoDeHeartbeat,
  Heartbeat,
  Hora,
  Incidente,
  IncidenteDeHeartbeat,
  ItemDoResumo,
  PaginaDeStatus,
  Sessao,
} from '@/lib/api/schemas'
import { Agendamento } from '@/lib/api/schemas'

// Uma API falsa, com estado, para o MSW. Ela imita as GUARDAS do servidor, e
// não só as respostas felizes; sem isso, os testes de cliente passariam
// vacuamente (lições de teste do projeto):
// - mutação sem X-Requested-With → 403 forbidden (RequireCustomHeader);
// - rota protegida sem Bearer válido → 401 unauthorized (Authn);
// - /auth/refresh rotaciona o cookie e detecta reúso: um cookie já consumido
//   revoga a família inteira (auth.Manager.Rotate).
//
// O "cookie" é o pote do navegador: um só por teste, como abas do mesmo
// navegador. O refresh lê o pote no INÍCIO da requisição e grava o cookie
// novo no FIM, depois do atraso — é o que deixa duas renovações se
// sobreporem como no navegador de verdade (spike do Plano 6, §8.2).

type Json = Record<string, unknown>

type Entrada<T extends z.ZodType> = z.input<T>

type Contexto = { request: Request; params: Record<string, string>; corpo: Json }
type Tratador = (a: Contexto) => Response | Promise<Response>

export const EMAIL = 'dono@exemplo.com'
export const SENHA = 'uma senha bem comprida'
export const TOTP_OK = '123456'
export const RECUPERACAO_OK = 'ABCDEFGHIJKLMNOP'
// O código do app NOVO na troca de autenticador (spec do Plano 9 §3.2).
export const TOTP_NOVO = '654321'
export const SEGREDO_NOVO = 'KRSXG5CTMVRXEZLU'
// Os 10 códigos de recuperação que a confirmação da troca entrega.
export const CODIGOS_NOVOS = Array.from({ length: 10 }, (_, i) => `NOVO${String(i).padStart(2, '0')}CODIGO000${i}`)
// O rotation do n-ésimo rotate (A7 do Plano 10): 32 dígitos hex, como o
// servidor (16 bytes derivados do segredo pendente).
export const PING_URL = 'https://stillon.exemplo/ping/CANARIO-do-token-de-ping'
export const rotacao = (n: number) => n.toString(16).padStart(32, '0')

const agora = () => new Date('2026-09-24T12:00:00Z')

// auth.FamilyProofLimit (= LockThreshold): a 5ª prova errada na mesma família
// de sessão a derruba (A3 do Plano 10).
export const LIMITE_DE_PROVAS = 5

const UMA_HORA_S = 3600

// A errorTable de internal/apierr/errors.go: código e status andam juntos, e o
// teste de contrato a compara com o Go.
export const ERROS_DO_SERVIDOR = {
  invalid_credentials: 401,
  invalid_session: 401,
  unauthorized: 401,
  not_found: 404,
  validation_failed: 400,
  rate_limited: 429,
  payload_too_large: 413,
  forbidden: 403,
  internal_error: 500,
  unavailable: 503,
  channel_limit_reached: 409,
  channel_not_pending: 409,
  delivery_failed: 502,
  status_page_exists: 409,
  slug_taken: 409,
  target_limit_reached: 409,
  content_rule_host_limit_reached: 409,
  heartbeat_limit_reached: 409,
  method_not_allowed: 405,
  schedule_unsupported: 409,
} as const

type ErrosDoServidor = typeof ERROS_DO_SERVIDOR
type CampoComErro = { field: string; reason: string }
export type ErroForcado = {
  [C in keyof ErrosDoServidor]: { status: ErrosDoServidor[C]; code: C; fields?: CampoComErro[] }
}[keyof ErrosDoServidor]

// rotulo.MaxTags, maxTagRunes e a allowlist de ParseTag; estadosDoFiltro de
// internal/targets/tags.go.
const MAX_TAGS = 8
const MAX_LETRAS_DA_TAG = 24
const TAG_VALIDA = /^[\p{L}\p{Nd}_-]+$/u
const ESTADOS_DO_FILTRO = ['up', 'degraded', 'down', 'unknown', 'paused']
const MAX_NOME_DO_HEARTBEAT = 100
const SUFIXO_DA_COPIA = ' (cópia)'

// Os DTOs de entrada do servidor (decodeJSON recusa o que não estiver aqui).
// Criar e editar aceitam os mesmos nomes em cada recurso.
const CAMPOS_DO_ALVO = [
  'name',
  'url',
  'method',
  'interval_sec',
  'timeout_ms',
  'expected_status',
  'degraded_threshold_ms',
  'enabled',
  'headers',
  'tags',
  'tls_warn',
  'content_rule',
]
const CAMPOS_DA_REGRA = { content_rule: ['mode', 'value', 'json_pointer'] }
const CAMPOS_DO_HEARTBEAT = ['name', 'description', 'tags', 'schedule', 'grace_sec', 'enabled', 'ping_resumes']
const CAMPOS_DO_AGENDAMENTO = ['kind', 'period_sec', 'expr', 'timezone']
const CAMPOS_DA_PAGINA = ['slug', 'title', 'published', 'items']
const CAMPOS_DO_ITEM_DA_PAGINA = ['target_id', 'display_name']
// Os campos do PATCH em que null é 400, na ordem de recusouNulo no servidor. Nos
// outros (tags, description, headers, degraded_threshold_ms), null limpa.
const OBRIGATORIOS_DO_ALVO = ['name', 'url', 'method', 'interval_sec', 'timeout_ms', 'expected_status', 'enabled', 'tls_warn']
const OBRIGATORIOS_DO_HEARTBEAT = ['name', 'schedule', 'grace_sec', 'enabled', 'ping_resumes']
const OBRIGATORIOS_DA_PAGINA = ['slug', 'title', 'published', 'items']
const PROVAS_DO_2FA = ['password', 'code', 'recovery_code']

export type ApiFalsa = ReturnType<typeof criarApiFalsa>

// toContentRuleResponse do servidor: sem regra é null, e json_pointer sai
// sempre, null fora de json_equals.
function regraDaResposta(corpo: unknown) {
  if (typeof corpo !== 'object' || corpo === null) return null
  const r = corpo as { mode: 'contains' | 'not_contains' | 'json_equals'; value: string; json_pointer?: string }
  return { mode: r.mode, value: r.value, json_pointer: r.json_pointer || null }
}

// Fora de um objeto (ausente, null) não há nome a recusar; o servidor trata o
// tipo errado à parte.
function temCampoAlheio(valor: unknown, permitidos: string[]) {
  const objetos: unknown[] = Array.isArray(valor) ? valor : [valor]
  return objetos.some(
    (o) => typeof o === 'object' && o !== null && Object.keys(o).some((nome) => !permitidos.includes(nome)),
  )
}

export function criarApiFalsa() {
  const st = {
    pote: null as string | null,
    consumidos: new Set<string>(),
    familiaRevogada: false,
    // A família de sessão do login atual; cada access token sabe a dele
    // (a claim sid do servidor).
    familia: 'f-atual',
    seq: 0,
    acessos: new Map<string, string>(),
    atrasoRefresh: 0,
    totpLigado: false,
    totpPendente: false,
    // Troca de autenticador em andamento: o segredo pendente do rotate
    // (spec do Plano 9 §3.2), com o rotation que o identifica (A7 do Plano
    // 10). Só o confirm, o disable e outro rotate o mexem.
    troca: null as { rotation: string; familia?: string } | null,
    rotacoes: 0,
    // Códigos de recuperação já gastos. Com trocaFalhaDepoisDoCodigo, a
    // troca de senha gasta o código e DEPOIS falha com 401 unauthorized, como
    // o servidor quando a família morre no meio da troca (pendência do
    // Plano 6: o código é consumido antes da transação).
    recuperacaoGasta: new Set<string>(),
    trocaFalhaDepoisDoCodigo: false,
    // Provas erradas por família de sessão (auth.FamilyProofs, A3 do Plano
    // 10): a troca de senha e as cinco rotas do 2FA dividem o contador. A 5ª
    // derruba a família (401 unauthorized); a prova certa zera.
    provasErradas: new Map<string, number>(),
    // O bloqueio da CONTA (users.locked_until): só o login e o /totp/verify o
    // olham. As rotas autenticadas deixaram de consultá-lo (A3 do Plano 10);
    // o rotate, o rotate/confirm e a troca de senha bem-sucedidos o zeram (A6;
    // SwapUserHash).
    contaBloqueada: false,
    // O desafio de 2FA do login ainda vale (a senha não mudou desde ele).
    desafioValido: false,
    chamadas: new Map<string, number>(),
    corpos: new Map<string, Json[]>(),
    alvos: [] as Entrada<typeof Alvo>[],
    resumo: [] as Entrada<typeof ItemDoResumo>[],
    // A query de cada GET /targets/summary: prova que o filtro foi à API, e
    // que a busca não vai.
    consultasDoResumo: [] as string[],
    serie: { resolution: 'raw', items: [] } as { resolution: 'raw'; items: Entrada<typeof Check>[] } | {
      resolution: '1h'
      items: Entrada<typeof Hora>[]
    },
    incidentes: [] as Entrada<typeof Incidente>[],
    heartbeats: [] as Entrada<typeof Heartbeat>[],
    // O last_signal do servidor, por heartbeat: ele não sai na resposta, e por
    // isso fica fora do item. Sem entrada, a tarefa nunca mandou sinal.
    ultimosSinais: new Map<string, 'start' | 'success' | 'fail'>(),
    // null responde 400 no campo expr.
    previaDoCron: ['2026-09-25T06:00:00Z', '2026-09-26T06:00:00Z'] as string[] | null,
    previasPedidas: [] as string[],
    eventos: [] as Entrada<typeof EventoDeHeartbeat>[],
    incidentesDeHeartbeat: [] as Entrada<typeof IncidenteDeHeartbeat>[],
    canais: [] as Entrada<typeof Canal>[],
    paginas: [] as Entrada<typeof PaginaDeStatus>[],
    sessoes: [] as Entrada<typeof Sessao>[],
    // Respostas forçadas por rota ("POST /targets" → 409 etc.).
    forcar: new Map<string, ErroForcado>(),
  }

  function contar(chave: string, corpo?: Json) {
    st.chamadas.set(chave, (st.chamadas.get(chave) ?? 0) + 1)
    if (corpo) st.corpos.set(chave, [...(st.corpos.get(chave) ?? []), corpo])
  }

  function responderErro({ status, code, fields }: { status: number; code: string; fields?: CampoComErro[] }) {
    return HttpResponse.json(
      { error: { code, message: 'mensagem do servidor', ...(fields ? { fields } : {}) }, trace_id: 'trace-falso' },
      { status },
    )
  }

  // O tipo só deixa escrever o par que o servidor emite.
  function erro<C extends keyof ErrosDoServidor>(status: ErrosDoServidor[C], code: C, fields?: CampoComErro[]) {
    return responderErro({ status, code, fields })
  }

  // Como as rotas de lista do servidor: next_cursor quando a página vem cheia.
  function paginar<T extends { id: string }>(itens: T[], request: Request) {
    const q = new URL(request.url).searchParams
    const limite = Number(q.get('limit') ?? 50)
    const inicio = q.get('cursor') ? itens.findIndex((i) => i.id === q.get('cursor')) + 1 : 0
    const pagina = itens.slice(inicio, inicio + limite)
    const cheia = pagina.length === limite
    return HttpResponse.json({ items: pagina, ...(cheia ? { next_cursor: pagina[pagina.length - 1]!.id } : {}) })
  }

  function novoAcesso() {
    st.seq++
    const t = `acesso-${st.seq}`
    st.acessos.set(t, st.familia)
    return t
  }

  function novoCookie() {
    st.seq++
    return `refresh-${st.seq}`
  }

  // Um login completo abre uma família nova (o refresh continua a dela).
  function abrirSessao() {
    st.pote = novoCookie()
    st.familia = `f-${++st.seq}`
    st.familiaRevogada = false
    return HttpResponse.json({ access_token: novoAcesso(), expires_in: 900 })
  }

  // A posse do fator (TOTP.Verify): o código do app, ou um código de
  // recuperação ainda não gasto, que fica gasto (E8 do Plano 10: a falsa
  // antiga aceitava o mesmo código de recuperação para sempre no disable e
  // no verify).
  function provaDoFator(codigo: string): boolean {
    if (codigo === TOTP_OK) return true
    if (codigo !== RECUPERACAO_OK || st.recuperacaoGasta.has(codigo)) return false
    st.recuperacaoGasta.add(codigo)
    return true
  }

  // decodeJSON: campo desconhecido, também em objeto aninhado, é 400 sem fields.
  // Sem isto, um cliente que mandasse um nome errado passaria nos testes de tela.
  function estrita(permitidos: string[], fn: Tratador, aninhados: Record<string, string[]> = {}): Tratador {
    return (a) => {
      const recusado =
        temCampoAlheio(a.corpo, permitidos) ||
        Object.entries(aninhados).some(([campo, nomes]) => temCampoAlheio(a.corpo[campo], nomes))
      return recusado ? erro(400, 'validation_failed') : fn(a)
    }
  }

  // ParseTag do servidor: a tag canônica, ou o motivo da recusa.
  function lerTag(crua: string) {
    const tag = crua.trim().toLowerCase()
    if ([...tag].length > MAX_LETRAS_DA_TAG) return { motivo: 'too_long' }
    return TAG_VALIDA.test(tag) ? { tag } : { motivo: 'invalid' }
  }

  // ParseSummaryFilter do servidor, mais a recusa de parâmetro desconhecido
  // do handler. Devolve os campos recusados e as tags canônicas.
  function lerFiltroDoResumo(q: URLSearchParams) {
    if ([...q.keys()].some((nome) => nome !== 'tag' && nome !== 'state')) {
      return { recusados: [{ field: 'query', reason: 'invalid' }], tags: [] }
    }
    const recusados: CampoComErro[] = []
    const lidas = q.getAll('tag').map(lerTag)
    const estados = q.getAll('state')
    const motivoDaTag = lidas.find((l) => l.motivo)?.motivo
    if (lidas.length > MAX_TAGS) recusados.push({ field: 'tag', reason: 'out_of_range' })
    else if (motivoDaTag) recusados.push({ field: 'tag', reason: motivoDaTag })
    if (estados.length > ESTADOS_DO_FILTRO.length) recusados.push({ field: 'state', reason: 'out_of_range' })
    else if (estados.some((e) => !ESTADOS_DO_FILTRO.includes(e))) recusados.push({ field: 'state', reason: 'invalid' })
    return { recusados, tags: lidas.flatMap((l) => l.tag ?? []) }
  }

  // recusouNulo: o 400 vem antes de procurar o recurso e lista todos os null.
  function recusaDeNulos(corpo: Json, obrigatorios: string[]) {
    const fields = obrigatorios.filter((c) => corpo[c] === null).map((field) => ({ field, reason: 'required' }))
    return fields.length > 0 ? erro(400, 'validation_failed', fields) : null
  }

  // A família do access token da requisição.
  const familiaDe = (request: Request) =>
    st.acessos.get(request.headers.get('Authorization')?.replace(/^Bearer /, '') ?? '')

  // provasDaFamilia.errou: até a 4ª, a resposta de sempre da rota; a 5ª
  // derruba a família (RevokeFamilyForWrongProofs) e responde 401
  // unauthorized. O cookie fica no navegador, mas a renovação falha.
  function provaErrada(request: Request, status: number, code: string) {
    const f = familiaDe(request) ?? ''
    const n = (st.provasErradas.get(f) ?? 0) + 1
    st.provasErradas.set(f, n)
    if (n < LIMITE_DE_PROVAS) return responderErro({ status, code })
    if (f === st.familia) st.familiaRevogada = true
    return erro(401, 'unauthorized')
  }

  // ProofReservation.Succeed: as provas pedidas conferiram, a família zera.
  const provaCerta = (request: Request) => st.provasErradas.delete(familiaDe(request) ?? '')

  // finishedARun do servidor: um início sozinho não prova uma execução.
  function terminouAlgumaExecucao(h: Entrada<typeof Heartbeat>) {
    const sinal = st.ultimosSinais.get(h.id)
    return h.last_success_at !== null || (sinal !== undefined && sinal !== 'start')
  }

  // statusOf do servidor. A falsa não tem failing_since: quem está em 'failing'
  // segue falhando. A retomada não precisa zerar nada, porque parte de 'paused'.
  function statusDe(h: Entrada<typeof Heartbeat>): Entrada<typeof Heartbeat>['state'] {
    if (!h.enabled) return 'paused'
    if (h.state === 'failing') return 'failing'
    if (h.open_incident_id !== null || new Date(h.deadline_at) <= agora()) return 'late'
    return terminouAlgumaExecucao(h) ? 'up' : 'waiting'
  }

  // FirstDeadline do servidor, com um período fixo: sempre no futuro.
  const primeiroPrazo = (graceSec: number) => new Date(agora().getTime() + (graceSec + UMA_HORA_S) * 1000).toISOString()

  // Schedule.Equal do servidor: campo a campo, para a ordem das chaves do corpo
  // não contar como mudança.
  function mesmoAgendamento(a: Json, b: Json) {
    return a.kind === b.kind && a.period_sec === b.period_sec && a.expr === b.expr && a.timezone === b.timezone
  }

  // updateParams do servidor: o prazo só recomeça se o agendamento ou a
  // tolerância mudarem de verdade.
  function mudouOPrazo(h: Entrada<typeof Heartbeat>, corpo: Json) {
    // A entrada do Agendamento é unknown (o preprocess do kind de versão
    // posterior); o parse devolve a forma que o servidor compararia.
    const atual: Json = { ...Agendamento.parse(h.schedule) }
    const outroAgendamento = corpo.schedule !== undefined && !mesmoAgendamento(corpo.schedule as Json, atual)
    return outroAgendamento || (corpo.grace_sec !== undefined && corpo.grace_sec !== h.grace_sec)
  }

  // Envolve um handler com as guardas do servidor. Num GET, o "corpo"
  // guardado é a query string (os testes da série leem o window).
  function rota(
    metodo: 'get' | 'post' | 'patch' | 'delete',
    caminho: string,
    fn: Tratador,
    protegida = true,
  ): HttpHandler {
    return http[metodo](`*/api/v1${caminho}`, async ({ request, params }) => {
      const chave = `${metodo.toUpperCase()} ${caminho}`
      const corpo = request.headers.get('Content-Type')?.includes('json') ? ((await request.json()) as Json) : {}
      const consulta = Object.fromEntries(new URL(request.url).searchParams)
      contar(chave, metodo === 'get' ? (Object.keys(consulta).length ? consulta : undefined) : corpo)
      if (metodo !== 'get' && request.headers.get('X-Requested-With') !== 'stillon') return erro(403, 'forbidden')
      if (protegida) {
        const t = request.headers.get('Authorization')?.replace(/^Bearer /, '')
        if (!t || !st.acessos.has(t)) return erro(401, 'unauthorized')
        // Toda rota protegida, inclusive /me e recursos, consulta a
        // família: assinatura e prazo do access token não bastam.
        if (st.familiaRevogada) return erro(401, 'unauthorized')
      }
      const f = st.forcar.get(chave)
      if (f) return responderErro(f)
      return fn({ request, params: params as Record<string, string>, corpo })
    })
  }

  const handlers: HttpHandler[] = [
    rota(
      'post',
      '/auth/login',
      estrita(['email', 'password'], ({ corpo }) => {
        // Conta bloqueada: o mesmo 401 genérico, sem olhar a senha (o cookie
        // de dispositivo, que a dispensaria, não está nesta falsa).
        if (st.contaBloqueada) return erro(401, 'invalid_credentials')
        if (corpo.email !== EMAIL || corpo.password !== SENHA) return erro(401, 'invalid_credentials')
        if (st.totpLigado) {
          st.desafioValido = true
          return HttpResponse.json({ totp_required: true, challenge: 'desafio-1' })
        }
        return abrirSessao()
      }),
      false,
    ),
    rota(
      'post',
      '/auth/totp/verify',
      estrita(['challenge', 'code'], ({ corpo }) => {
        if (corpo.challenge !== 'desafio-1') return erro(401, 'unauthorized')
        // Task 11 do Plano 5: a senha mudou depois do desafio → a senha
        // verificada é velha, e o verify responde invalid_credentials.
        if (!st.desafioValido) return erro(401, 'invalid_credentials')
        if (st.contaBloqueada) return erro(401, 'invalid_credentials')
        if (!provaDoFator(String(corpo.code ?? ''))) return erro(401, 'invalid_credentials')
        return abrirSessao()
      }),
      false,
    ),
    rota(
      'post',
      '/auth/refresh',
      async () => {
        const apresentado = st.pote
        await delay(st.atrasoRefresh)
        if (!apresentado || st.familiaRevogada) return erro(401, 'invalid_session')
        if (st.consumidos.has(apresentado)) {
          st.familiaRevogada = true
          st.pote = null
          return erro(401, 'invalid_session')
        }
        st.consumidos.add(apresentado)
        st.pote = novoCookie()
        return HttpResponse.json({ access_token: novoAcesso(), expires_in: 900 })
      },
      false,
    ),
    rota(
      'post',
      '/auth/logout',
      () => {
        st.familiaRevogada = true
        st.pote = null
        return new HttpResponse(null, { status: 204 })
      },
      false,
    ),
    rota('get', '/me', () =>
      HttpResponse.json({ id: 'u1', email: EMAIL, totp_enabled: st.totpLigado, created_at: '2026-09-01T10:00:00Z' }),
    ),

    // O filtro do servidor (targets.SummaryFilter): tags em E, estados em OU.
    rota('get', '/targets/summary', ({ request }) => {
      const q = new URL(request.url).searchParams
      st.consultasDoResumo.push(q.toString())
      const forcado = st.forcar.get(`GET /targets/summary?${q.toString()}`)
      if (forcado) return responderErro(forcado)
      const { recusados, tags } = lerFiltroDoResumo(q)
      if (recusados.length > 0) return erro(400, 'validation_failed', recusados)
      const estados = q.getAll('state')
      const items = st.resumo.filter(
        (it) =>
          tags.every((t) => it.tags.includes(t)) &&
          (estados.length === 0 || estados.includes(it.enabled ? it.state : 'paused')),
      )
      return HttpResponse.json({ items })
    }),
    rota('get', '/targets', ({ request }) => paginar(st.alvos, request)),
    rota('post', '/targets', estrita(CAMPOS_DO_ALVO, ({ corpo }) => {
      const alvo = {
        id: `alvo-${st.alvos.length + 1}`,
        name: String(corpo.name),
        url: String(corpo.url),
        method: corpo.method as 'GET',
        interval_sec: Number(corpo.interval_sec),
        timeout_ms: Number(corpo.timeout_ms),
        expected_status: Number(corpo.expected_status),
        degraded_threshold_ms: corpo.degraded_threshold_ms === undefined ? 2000 : (corpo.degraded_threshold_ms as number | null),
        enabled: corpo.enabled !== false,
        header_names: Object.keys((corpo.headers as Json | undefined) ?? {}).sort(),
        tags: (corpo.tags as string[] | undefined) ?? [],
        // Como o servidor: o aviso de certificado nasce ligado, e a leitura
        // só existe depois da primeira sonda.
        tls_warn: corpo.tls_warn !== false,
        tls: null,
        content_rule: regraDaResposta(corpo.content_rule),
        created_at: agora().toISOString(),
      }
      st.alvos.push(alvo)
      return HttpResponse.json(alvo, { status: 201 })
    }, CAMPOS_DA_REGRA)),
    rota('get', '/targets/:id', ({ params }) => {
      const a = st.alvos.find((x) => x.id === params.id)
      return a ? HttpResponse.json(a) : erro(404, 'not_found')
    }),
    rota('patch', '/targets/:id', estrita(CAMPOS_DO_ALVO, ({ params, corpo }) => {
      const recusa = recusaDeNulos(corpo, OBRIGATORIOS_DO_ALVO)
      if (recusa) return recusa
      const a = st.alvos.find((x) => x.id === params.id)
      if (!a) return erro(404, 'not_found')
      const { headers, content_rule, ...resto } = corpo
      // B1 do Plano 10: trocar a origem da URL (esquema, host ou porta) sem
      // mandar headers apaga os headers; só o caminho, não.
      const outraOrigem =
        typeof resto.url === 'string' && new URL(resto.url.trim()).origin !== new URL(a.url.trim()).origin
      Object.assign(a, resto)
      if (headers !== undefined) a.header_names = Object.keys((headers as Json | null) ?? {}).sort()
      else if (outraOrigem) a.header_names = []
      // Como mudouDeDestino no servidor: outra origem é outro certificado, e a
      // leitura do anterior sai junto.
      if (outraOrigem) a.tls = null
      if (content_rule !== undefined) a.content_rule = regraDaResposta(content_rule)
      return HttpResponse.json(a)
    }, CAMPOS_DA_REGRA)),
    rota('delete', '/targets/:id', ({ params }) => {
      // Como o servidor (TargetHandlers.Delete): alvo que não existe é 404.
      if (!st.alvos.some((x) => x.id === params.id)) return erro(404, 'not_found')
      st.alvos = st.alvos.filter((x) => x.id !== params.id)
      st.resumo = st.resumo.filter((x) => x.id !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
    // B6 do Plano 10: a janela vai por nome (?window=), e o servidor calcula
    // o from com o relógio dele. window fora da lista, vazio, ou junto com
    // from é 400 (field window, reason invalid).
    rota('get', '/targets/:id/checks', ({ request }) => {
      const q = new URL(request.url).searchParams
      const janela = q.get('window')
      if (janela !== null && (!['1h', '6h', '24h', '7d', '30d', '90d'].includes(janela) || q.has('from'))) {
        return erro(400, 'validation_failed', [{ field: 'window', reason: 'invalid' }])
      }
      return HttpResponse.json(st.serie)
    }),

    rota('get', '/incidents', ({ request }) => {
      const q = new URL(request.url).searchParams
      let itens = st.incidentes
      if (q.get('status') === 'open') itens = itens.filter((i) => i.resolved_at === null)
      if (q.get('status') === 'resolved') itens = itens.filter((i) => i.resolved_at !== null)
      if (q.get('target_id')) itens = itens.filter((i) => i.target_id === q.get('target_id'))
      return paginar(itens, request)
    }),
    rota('post', '/incidents/:id/ack', ({ params }) => {
      const i = st.incidentes.find((x) => x.id === params.id)
      if (!i) return erro(404, 'not_found')
      i.acknowledged_at ??= agora().toISOString()
      return HttpResponse.json(i)
    }),

    rota('get', '/heartbeats', ({ request }) => paginar(st.heartbeats, request)),
    rota('post', '/heartbeats', estrita(CAMPOS_DO_HEARTBEAT, ({ corpo }) => {
      const h = {
        id: `hb-${st.heartbeats.length + 1}`,
        name: String(corpo.name),
        description: (corpo.description as string | undefined) ?? '',
        tags: (corpo.tags as string[] | undefined) ?? [],
        enabled: true,
        schedule: corpo.schedule as Entrada<typeof Heartbeat>['schedule'],
        grace_sec: corpo.grace_sec === undefined ? 300 : Number(corpo.grace_sec),
        ping_resumes: corpo.ping_resumes === true,
        state: 'waiting' as const,
        deadline_at: primeiroPrazo(corpo.grace_sec === undefined ? 300 : Number(corpo.grace_sec)),
        last_ping_at: null,
        last_success_at: null,
        last_start_at: null,
        last_runtime_ms: null,
        open_incident_id: null,
        created_at: agora().toISOString(),
      }
      st.heartbeats.push(h)
      return HttpResponse.json({ ...h, ping_url: PING_URL }, { status: 201 })
    }, { schedule: CAMPOS_DO_AGENDAMENTO })),
    rota('get', '/heartbeats/cron-preview', ({ request }) => {
      st.previasPedidas.push(new URL(request.url).search)
      return st.previaDoCron === null
        ? erro(400, 'validation_failed', [{ field: 'expr', reason: 'invalid' }])
        : HttpResponse.json({ next_runs: st.previaDoCron })
    }),
    rota('get', '/heartbeats/:id', ({ params }) => {
      const h = st.heartbeats.find((x) => x.id === params.id)
      return h ? HttpResponse.json(h) : erro(404, 'not_found')
    }),
    rota('patch', '/heartbeats/:id', estrita(CAMPOS_DO_HEARTBEAT, ({ params, corpo }) => {
      const recusa = recusaDeNulos(corpo, OBRIGATORIOS_DO_HEARTBEAT)
      if (recusa) return recusa
      const h = st.heartbeats.find((x) => x.id === params.id)
      if (!h) return erro(404, 'not_found')
      const retomou = corpo.enabled === true && !h.enabled
      const recomecaOPrazo = retomou || mudouOPrazo(h, corpo)
      Object.assign(h, corpo)
      if (retomou) h.last_start_at = null
      if (recomecaOPrazo) h.deadline_at = primeiroPrazo(h.grace_sec)
      h.state = statusDe(h)
      return HttpResponse.json(h)
    }, { schedule: CAMPOS_DO_AGENDAMENTO })),
    rota('delete', '/heartbeats/:id', ({ params }) => {
      if (!st.heartbeats.some((x) => x.id === params.id)) return erro(404, 'not_found')
      st.heartbeats = st.heartbeats.filter((x) => x.id !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
    rota('get', '/heartbeats/:id/events', ({ request, params }) => {
      if (!st.heartbeats.some((x) => x.id === params.id)) return erro(404, 'not_found')
      return paginar(st.eventos, request)
    }),
    rota('get', '/heartbeats/:id/incidents', ({ request, params }) => {
      if (!st.heartbeats.some((x) => x.id === params.id)) return erro(404, 'not_found')
      return paginar(st.incidentesDeHeartbeat.filter((i) => i.heartbeat_id === params.id), request)
    }),
    // O ack é idempotente, como o do alvo: reconhecer de novo não muda a data.
    rota('post', '/heartbeats/:id/incidents/:incident/ack', ({ params }) => {
      const i = st.incidentesDeHeartbeat.find((x) => x.id === params.incident && x.heartbeat_id === params.id)
      if (!i) return erro(404, 'not_found')
      i.acknowledged_at ??= agora().toISOString()
      return HttpResponse.json(i)
    }),
    // O clone do servidor: sem token, eventos, last_* nem incidentes; o nome
    // cortado para o sufixo caber em 100 caracteres (Rulings do PR 2 da SP1).
    rota('post', '/heartbeats/:id/clone', ({ params }) => {
      const h = st.heartbeats.find((x) => x.id === params.id)
      if (!h) return erro(404, 'not_found')
      if (Agendamento.parse(h.schedule).kind === 'unknown') return erro(409, 'schedule_unsupported')
      const copia = {
        ...h,
        id: `hb-${st.heartbeats.length + 1}`,
        name: `${Array.from(h.name).slice(0, MAX_NOME_DO_HEARTBEAT - SUFIXO_DA_COPIA.length).join('')}${SUFIXO_DA_COPIA}`,
        state: h.enabled ? ('waiting' as const) : ('paused' as const),
        deadline_at: primeiroPrazo(h.grace_sec),
        last_ping_at: null,
        last_success_at: null,
        last_start_at: null,
        last_runtime_ms: null,
        open_incident_id: null,
        created_at: agora().toISOString(),
      }
      st.heartbeats.push(copia)
      return HttpResponse.json({ ...copia, ping_url: `${PING_URL}-copia` }, { status: 201 })
    }),
    // Limpar o que já está vazio também é 204.
    rota('post', '/heartbeats/:id/events/clear', ({ params }) => {
      if (!st.heartbeats.some((x) => x.id === params.id)) return erro(404, 'not_found')
      st.eventos = []
      return new HttpResponse(null, { status: 204 })
    }),
    // PingNow do servidor: um sucesso com source 'panel'. O log ganha a linha,
    // o prazo recomeça e o sucesso tira o failing. O incidente aberto fica
    // (registrarSinal não mexe nele), e com ele o statusDe segue em late.
    rota('post', '/heartbeats/:id/ping-now', ({ params }) => {
      const h = st.heartbeats.find((x) => x.id === params.id)
      if (!h) return erro(404, 'not_found')
      const quando = agora().toISOString()
      st.eventos = [
        { id: `ev-painel-${st.eventos.length + 1}`, at: quando, kind: 'success', runtime_ms: null, exit_code: null, run_id: null, source: 'panel' },
        ...st.eventos,
      ]
      st.ultimosSinais.set(h.id, 'success')
      Object.assign(h, {
        last_ping_at: quando,
        last_success_at: quando,
        last_start_at: null,
        deadline_at: primeiroPrazo(h.grace_sec),
        state: 'up',
      })
      h.state = statusDe(h)
      return HttpResponse.json(h)
    }),
    rota('post', '/heartbeats/:id/token/rotate', ({ params }) => {
      const h = st.heartbeats.find((x) => x.id === params.id)
      if (!h) return erro(404, 'not_found')
      return HttpResponse.json({ ...h, ping_url: `${PING_URL}-novo` })
    }),

    rota('get', '/channels', () => HttpResponse.json({ items: st.canais })),
    rota('post', '/channels', estrita(['kind', 'url', 'email'], ({ corpo }) => {
      const email = corpo.kind === 'email'
      const c = {
        id: `canal-${st.canais.length + 1}`,
        kind: email ? ('email' as const) : ('discord' as const),
        hint: email ? 'd***@exemplo.com' : 'discord.com/…/abcd',
        status: email ? ('pending' as const) : ('verified' as const),
        enabled: true,
        created_at: agora().toISOString(),
        last_error: null,
        last_failure_at: null,
      }
      st.canais.push(c)
      return HttpResponse.json(c, { status: 201 })
    })),
    // ChannelHandlers.Delete e Resend: canal que não existe é 404.
    rota('delete', '/channels/:id', ({ params }) => {
      if (!st.canais.some((c) => c.id === params.id)) return erro(404, 'not_found')
      st.canais = st.canais.filter((c) => c.id !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
    rota('post', '/channels/:id/verify/resend', ({ params }) => {
      const c = st.canais.find((x) => x.id === params.id)
      if (!c) return erro(404, 'not_found')
      // ResendVerification: só e-mail ainda não verificado. O expired é um deles:
      // o token venceu, e reenviar é o conserto. O failing já foi verificado.
      if (c.kind !== 'email' || c.status === 'verified' || c.status === 'failing') return erro(409, 'channel_not_pending')
      return new HttpResponse(null, { status: 204 })
    }),
    // ChannelHandlers.SetEnabled (spec do Plano 9 §3.3): o corpo é lido
    // ANTES de procurar o canal. Campo desconhecido é 400 sem fields
    // (decodeJSONStrict: o nome vem do cliente e não ecoa); sem enabled é
    // required; enabled que não é booleano é invalid. Depois, canal que não
    // existe (ou de outro usuário) é 404. Mudar para o valor que já está é
    // 200 sem efeito.
    rota('patch', '/channels/:id', ({ params, corpo }) => {
      const chaves = Object.keys(corpo)
      if (chaves.some((k) => k !== 'enabled')) return erro(400, 'validation_failed')
      if (typeof corpo.enabled !== 'boolean') {
        return erro(400, 'validation_failed', [{ field: 'enabled', reason: chaves.includes('enabled') ? 'invalid' : 'required' }])
      }
      const c = st.canais.find((x) => x.id === params.id)
      if (!c) return erro(404, 'not_found')
      c.enabled = corpo.enabled
      return HttpResponse.json(c)
    }),

    rota('get', '/status-pages', () => HttpResponse.json({ items: st.paginas })),
    rota('post', '/status-pages', estrita(CAMPOS_DA_PAGINA, ({ corpo }) => {
      if (st.paginas.length > 0) return erro(409, 'status_page_exists')
      const p = {
        id: 'pagina-1',
        slug: String(corpo.slug),
        title: String(corpo.title),
        published: Boolean(corpo.published),
        items: (corpo.items as { target_id: string; display_name: string }[]) ?? [],
        public_url: `http://localhost:5173/p/${String(corpo.slug)}`,
        created_at: agora().toISOString(),
      }
      st.paginas = [p]
      return HttpResponse.json(p, { status: 201 })
    }, { items: CAMPOS_DO_ITEM_DA_PAGINA })),
    rota('patch', '/status-pages/:id', estrita(CAMPOS_DA_PAGINA, ({ params, corpo }) => {
      const recusa = recusaDeNulos(corpo, OBRIGATORIOS_DA_PAGINA)
      if (recusa) return recusa
      const p = st.paginas.find((x) => x.id === params.id)
      if (!p) return erro(404, 'not_found')
      Object.assign(p, corpo)
      p.public_url = `http://localhost:5173/p/${p.slug}`
      return HttpResponse.json(p)
    }, { items: CAMPOS_DO_ITEM_DA_PAGINA })),

    rota('get', '/sessions', () => HttpResponse.json({ items: st.sessoes })),
    rota('delete', '/sessions/:id', ({ params }) => {
      const s = st.sessoes.find((x) => x.id === params.id)
      if (!s) return erro(404, 'not_found')
      st.sessoes = st.sessoes.filter((x) => x.id !== params.id)
      // A própria família: o 204 já apaga o cookie (D15 do Plano 5).
      if (s.current) {
        st.familiaRevogada = true
        st.pote = null
      }
      return new HttpResponse(null, { status: 204 })
    }),
    // account.ChangePassword: a política da senha nova vem ANTES da senha
    // atual (e do código, que então não é gasto).
    // A prova errada conta na família (a política não: a reserva volta).
    rota('post', '/auth/password', estrita(['current_password', 'new_password', 'totp_code'], ({ request, corpo }) => {
      const n = [...String(corpo.new_password)].length
      if (n < 15) return erro(400, 'validation_failed', [{ field: 'new_password', reason: 'too_short' }])
      if (corpo.current_password !== SENHA) return provaErrada(request, 401, 'invalid_credentials')
      if (st.totpLigado) {
        const codigo = String(corpo.totp_code ?? '')
        if (!provaDoFator(codigo)) return provaErrada(request, 401, 'invalid_credentials')
        if (codigo === RECUPERACAO_OK && st.trocaFalhaDepoisDoCodigo) return erro(401, 'unauthorized')
      }
      provaCerta(request)
      st.contaBloqueada = false
      st.sessoes = st.sessoes.filter((s) => s.current)
      // Task 11 do Plano 5: trocar a senha apaga o cadastro de 2FA pendente
      // e invalida os desafios de login em aberto; D6 do Plano 9, a troca de
      // autenticador pendente também (SwapUserHash).
      st.totpPendente = false
      st.troca = null
      st.desafioValido = false
      return new HttpResponse(null, { status: 204 })
    })),
    rota('post', '/auth/totp/enroll', estrita(['password'], ({ request, corpo }) => {
      if (corpo.password !== SENHA) return provaErrada(request, 401, 'invalid_credentials')
      // Com o 2FA ligado, a senha certa NÃO zera a família (C1 da revisão do
      // Plano 10): 403, e a reserva volta. Só o cadastro feito zera.
      if (st.totpLigado) return erro(403, 'forbidden')
      provaCerta(request)
      st.totpPendente = true
      return HttpResponse.json({ otpauth_uri: 'otpauth://totp/StillOn:dono%40exemplo.com?secret=JBSWY3DPEHPK3PXP&issuer=StillOn' })
    })),
    // TOTP.Confirm com o 2FA já ligado é 403 forbidden ANTES de olhar o
    // código (C1 da revisão do Plano 10): o mesmo 403 para o certo e o
    // errado, sem contar nem zerar a família. Com o cadastro pendente, o
    // código errado é prova errada, 400 validation_failed, e conta (A3 do
    // Plano 10, que fechou o S5). Sem cadastro não há código a errar: 400
    // sem contar.
    rota('post', '/auth/totp/confirm', estrita(['code'], ({ request, corpo }) => {
      if (st.totpLigado) return erro(403, 'forbidden')
      if (!st.totpPendente) return erro(400, 'validation_failed')
      if (corpo.code !== TOTP_OK) return provaErrada(request, 400, 'validation_failed')
      provaCerta(request)
      st.totpLigado = true
      st.totpPendente = false
      return HttpResponse.json({ recovery_codes: ['AAAABBBBCCCCDDDD', 'EEEEFFFFGGGGHHHH'] })
    })),
    rota('post', '/auth/totp/disable', estrita(PROVAS_DO_2FA, ({ request, corpo }) => {
      // D24 do Plano 5: senha ou código errado é invalid_credentials;
      // unauthorized fica só para o token recusado (e para a 5ª prova errada
      // da família). Com o 2FA desligado, o Disable devolve
      // ErrTOTPNotEnrolled, que é o mesmo 401 e também conta.
      if (corpo.password !== SENHA || !st.totpLigado) return provaErrada(request, 401, 'invalid_credentials')
      if (!provaDoFator(String(corpo.code || corpo.recovery_code || ''))) {
        return provaErrada(request, 401, 'invalid_credentials')
      }
      provaCerta(request)
      st.totpLigado = false
      // O disable apaga a troca pendente (spec do Plano 9 §3.2).
      st.troca = null
      return new HttpResponse(null, { status: 204 })
    })),
    // Spec do Plano 9 §3.2: as provas do disable (senha e código do app ou
    // de recuperação). 2FA desligado é 403; prova errada é
    // invalid_credentials (D24 do Plano 5). O código de recuperação fica
    // gasto mesmo que a troca não seja confirmada.
    // TOTPRotate: a senha (senhaConfere) antes de tudo; depois, 2FA
    // desligado é 403, e só então a prova do fator.
    rota('post', '/auth/totp/rotate', estrita(PROVAS_DO_2FA, ({ request, corpo }) => {
      if (corpo.password !== SENHA) return provaErrada(request, 401, 'invalid_credentials')
      // 403 sem contar e sem zerar: a reserva volta (Release).
      if (!st.totpLigado) return erro(403, 'forbidden')
      if (!provaDoFator(String(corpo.code || corpo.recovery_code || ''))) {
        return provaErrada(request, 401, 'invalid_credentials')
      }
      provaCerta(request)
      st.contaBloqueada = false
      // Outro rotate substitui o pendente, e o rotation muda com ele. O
      // pendente é da família do token (totp_pending_family_id).
      st.troca = { rotation: rotacao(++st.rotacoes), familia: familiaDe(request) }
      return HttpResponse.json({
        otpauth_uri: `otpauth://totp/StillOn:dono%40exemplo.com?secret=${SEGREDO_NOVO}&issuer=StillOn`,
        rotation: st.troca.rotation,
      })
    })),
    // Sem troca pendente, ou com o rotation de um pendente que outro rotate
    // já substituiu (A7 do Plano 10), é 403, sem contar falha; código errado
    // é 401 e o pendente fica. O sucesso encerra as OUTRAS sessões e entrega
    // 10 códigos novos.
    rota('post', '/auth/totp/rotate/confirm', estrita(['code', 'rotation'], ({ request, corpo }) => {
      // O pendente de outra família é, para esta, "nenhuma troca". Um
      // pendente posto à mão pelo teste (sem família) vale para qualquer uma.
      const deOutra = st.troca?.familia !== undefined && st.troca.familia !== familiaDe(request)
      if (!st.troca || deOutra || corpo.rotation !== st.troca.rotation) return erro(403, 'forbidden')
      if (corpo.code !== TOTP_NOVO) return provaErrada(request, 401, 'invalid_credentials')
      provaCerta(request)
      st.contaBloqueada = false
      st.troca = null
      st.sessoes = st.sessoes.filter((s) => s.current)
      return HttpResponse.json({ recovery_codes: CODIGOS_NOVOS })
    })),
  ]

  return {
    handlers,
    estado: st,
    chamadas: (rotaChave: string) => st.chamadas.get(rotaChave) ?? 0,
    corpos: (rotaChave: string) => st.corpos.get(rotaChave) ?? [],
    // O navegador tem um refresh válido no pote: o boot entra sozinho.
    logado() {
      st.pote = novoCookie()
      st.familiaRevogada = false
    },
    // Os access tokens venceram (15 min); o refresh continua valendo.
    expirarAcessos() {
      st.acessos.clear()
    },
  }
}
