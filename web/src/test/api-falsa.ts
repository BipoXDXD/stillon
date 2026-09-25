import { delay, http, HttpResponse, type HttpHandler } from 'msw'
import type { z } from 'zod'
import type {
  Alvo,
  Canal,
  Check,
  Hora,
  Incidente,
  ItemDoResumo,
  PaginaDeStatus,
  Sessao,
} from '@/lib/api/schemas'

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
export const rotacao = (n: number) => n.toString(16).padStart(32, '0')

const agora = () => new Date('2026-09-24T12:00:00Z')

export type ApiFalsa = ReturnType<typeof criarApiFalsa>

export function criarApiFalsa() {
  const st = {
    pote: null as string | null,
    consumidos: new Set<string>(),
    familiaRevogada: false,
    familia: 'f-atual',
    seq: 0,
    acessos: new Set<string>(),
    atrasoRefresh: 0,
    totpLigado: false,
    totpPendente: false,
    // Troca de autenticador em andamento: o segredo pendente do rotate
    // (spec do Plano 9 §3.2), com o rotation que o identifica (A7 do Plano
    // 10). Só o confirm, o disable e outro rotate o mexem.
    troca: null as { rotation: string } | null,
    rotacoes: 0,
    // Códigos de recuperação já gastos. Com trocaFalhaDepoisDoCodigo, a
    // troca de senha gasta o código e DEPOIS falha com 401 unauthorized, como
    // o servidor quando a família morre no meio da troca (PENDENCIAS do
    // Plano 6: o código é consumido antes da transação).
    recuperacaoGasta: new Set<string>(),
    trocaFalhaDepoisDoCodigo: false,
    // O desafio de 2FA do login ainda vale (a senha não mudou desde ele).
    desafioValido: false,
    chamadas: new Map<string, number>(),
    corpos: new Map<string, Json[]>(),
    alvos: [] as Entrada<typeof Alvo>[],
    resumo: [] as Entrada<typeof ItemDoResumo>[],
    serie: { resolution: 'raw', items: [] } as { resolution: 'raw'; items: Entrada<typeof Check>[] } | {
      resolution: '1h'
      items: Entrada<typeof Hora>[]
    },
    incidentes: [] as Entrada<typeof Incidente>[],
    canais: [] as Entrada<typeof Canal>[],
    paginas: [] as Entrada<typeof PaginaDeStatus>[],
    sessoes: [] as Entrada<typeof Sessao>[],
    // Respostas forçadas por rota ("POST /targets" → 409 etc.).
    forcar: new Map<string, { status: number; code: string; fields?: { field: string; reason: string }[] }>(),
  }

  function contar(chave: string, corpo?: Json) {
    st.chamadas.set(chave, (st.chamadas.get(chave) ?? 0) + 1)
    if (corpo) st.corpos.set(chave, [...(st.corpos.get(chave) ?? []), corpo])
  }

  function erro(status: number, code: string, fields?: { field: string; reason: string }[]) {
    return HttpResponse.json(
      { error: { code, message: 'mensagem do servidor', ...(fields ? { fields } : {}) }, trace_id: 'trace-falso' },
      { status },
    )
  }

  function novoAcesso() {
    st.seq++
    const t = `acesso-${st.seq}`
    st.acessos.add(t)
    return t
  }

  function novoCookie() {
    st.seq++
    return `refresh-${st.seq}`
  }

  function abrirSessao() {
    st.pote = novoCookie()
    st.familiaRevogada = false
    return HttpResponse.json({ access_token: novoAcesso(), expires_in: 900 })
  }

  // As rotas que exigem a família do token viva (Plano 5: /auth/password e
  // /sessions; Task 11 do Plano 5: os três do 2FA). Um access token ainda
  // válido de uma família revogada passa pelo Authn e morre aqui, com 401
  // unauthorized — antes de a rota olhar senha ou código.
  const VIVAS = new Set([
    'POST /auth/password',
    'POST /auth/totp/enroll',
    'POST /auth/totp/confirm',
    'POST /auth/totp/disable',
    'POST /auth/totp/rotate',
    'POST /auth/totp/rotate/confirm',
    'GET /sessions',
    'DELETE /sessions/:id',
  ])

  // Envolve um handler com as guardas do servidor.
  function rota(
    metodo: 'get' | 'post' | 'patch' | 'delete',
    caminho: string,
    fn: (a: { request: Request; params: Record<string, string>; corpo: Json }) => Response | Promise<Response>,
    protegida = true,
  ): HttpHandler {
    return http[metodo](`*/api/v1${caminho}`, async ({ request, params }) => {
      const chave = `${metodo.toUpperCase()} ${caminho}`
      const corpo = request.headers.get('Content-Type')?.includes('json') ? ((await request.json()) as Json) : {}
      contar(chave, corpo)
      if (metodo !== 'get' && request.headers.get('X-Requested-With') !== 'vigilume') return erro(403, 'forbidden')
      if (protegida) {
        const t = request.headers.get('Authorization')?.replace(/^Bearer /, '')
        if (!t || !st.acessos.has(t)) return erro(401, 'unauthorized')
        if (VIVAS.has(chave) && st.familiaRevogada) return erro(401, 'unauthorized')
      }
      const f = st.forcar.get(chave)
      if (f) return erro(f.status, f.code, f.fields)
      return fn({ request, params: params as Record<string, string>, corpo })
    })
  }

  const handlers: HttpHandler[] = [
    rota(
      'post',
      '/auth/login',
      ({ corpo }) => {
        if (corpo.email !== EMAIL || corpo.password !== SENHA) return erro(401, 'invalid_credentials')
        if (st.totpLigado) {
          st.desafioValido = true
          return HttpResponse.json({ totp_required: true, challenge: 'desafio-1' })
        }
        return abrirSessao()
      },
      false,
    ),
    rota(
      'post',
      '/auth/totp/verify',
      ({ corpo }) => {
        if (corpo.challenge !== 'desafio-1') return erro(401, 'unauthorized')
        // Task 11 do Plano 5: a senha mudou depois do desafio → a senha
        // verificada é velha, e o verify responde invalid_credentials.
        if (!st.desafioValido) return erro(401, 'invalid_credentials')
        if (corpo.code !== TOTP_OK && corpo.code !== RECUPERACAO_OK) return erro(401, 'invalid_credentials')
        return abrirSessao()
      },
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

    rota('get', '/targets/summary', () => HttpResponse.json({ items: st.resumo })),
    rota('get', '/targets', () => HttpResponse.json({ items: st.alvos })),
    rota('post', '/targets', ({ corpo }) => {
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
        created_at: agora().toISOString(),
      }
      st.alvos.push(alvo)
      return HttpResponse.json(alvo, { status: 201 })
    }),
    rota('get', '/targets/:id', ({ params }) => {
      const a = st.alvos.find((x) => x.id === params.id)
      return a ? HttpResponse.json(a) : erro(404, 'not_found')
    }),
    rota('patch', '/targets/:id', ({ params, corpo }) => {
      const a = st.alvos.find((x) => x.id === params.id)
      if (!a) return erro(404, 'not_found')
      const { headers, ...resto } = corpo
      // B1 do Plano 10: trocar a origem da URL (esquema, host ou porta) sem
      // mandar headers apaga os headers; só o caminho, não.
      const outraOrigem =
        typeof resto.url === 'string' && new URL(resto.url.trim()).origin !== new URL(a.url.trim()).origin
      Object.assign(a, resto)
      if (headers !== undefined) a.header_names = Object.keys((headers as Json | null) ?? {}).sort()
      else if (outraOrigem) a.header_names = []
      return HttpResponse.json(a)
    }),
    rota('delete', '/targets/:id', ({ params }) => {
      // Como o servidor (TargetHandlers.Delete): alvo que não existe é 404.
      if (!st.alvos.some((x) => x.id === params.id)) return erro(404, 'not_found')
      st.alvos = st.alvos.filter((x) => x.id !== params.id)
      st.resumo = st.resumo.filter((x) => x.id !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
    rota('get', '/targets/:id/checks', () => HttpResponse.json(st.serie)),

    rota('get', '/incidents', ({ request }) => {
      const q = new URL(request.url).searchParams
      let itens = st.incidentes
      if (q.get('status') === 'open') itens = itens.filter((i) => i.resolved_at === null)
      if (q.get('status') === 'resolved') itens = itens.filter((i) => i.resolved_at !== null)
      if (q.get('target_id')) itens = itens.filter((i) => i.target_id === q.get('target_id'))
      const limite = Number(q.get('limit') ?? 50)
      const inicio = q.get('cursor') ? itens.findIndex((i) => i.id === q.get('cursor')) + 1 : 0
      const pagina = itens.slice(inicio, inicio + limite)
      const cheia = pagina.length === limite && inicio + limite < itens.length
      return HttpResponse.json({ items: pagina, ...(cheia ? { next_cursor: pagina[pagina.length - 1]!.id } : {}) })
    }),
    rota('post', '/incidents/:id/ack', ({ params }) => {
      const i = st.incidentes.find((x) => x.id === params.id)
      if (!i) return erro(404, 'not_found')
      i.acknowledged_at ??= agora().toISOString()
      return HttpResponse.json(i)
    }),

    rota('get', '/channels', () => HttpResponse.json({ items: st.canais })),
    rota('post', '/channels', ({ corpo }) => {
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
    }),
    rota('delete', '/channels/:id', ({ params }) => {
      st.canais = st.canais.filter((c) => c.id !== params.id)
      return new HttpResponse(null, { status: 204 })
    }),
    rota('post', '/channels/:id/verify/resend', () => new HttpResponse(null, { status: 204 })),
    // Spec do Plano 9 §3.3: o corpo é só {enabled: bool}; qualquer outra
    // coisa é 400 validation_failed. Canal que não existe (ou de outro
    // usuário) é 404. Mudar para o valor que já está é 200 sem efeito.
    rota('patch', '/channels/:id', ({ params, corpo }) => {
      const c = st.canais.find((x) => x.id === params.id)
      if (!c) return erro(404, 'not_found')
      const chaves = Object.keys(corpo)
      if (typeof corpo.enabled !== 'boolean') {
        return erro(400, 'validation_failed', [{ field: 'enabled', reason: chaves.includes('enabled') ? 'invalid' : 'required' }])
      }
      const outro = chaves.find((k) => k !== 'enabled')
      if (outro) return erro(400, 'validation_failed', [{ field: outro, reason: 'unknown' }])
      c.enabled = corpo.enabled
      return HttpResponse.json(c)
    }),

    rota('get', '/status-pages', () => HttpResponse.json({ items: st.paginas })),
    rota('post', '/status-pages', ({ corpo }) => {
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
    }),
    rota('patch', '/status-pages/:id', ({ params, corpo }) => {
      const p = st.paginas.find((x) => x.id === params.id)
      if (!p) return erro(404, 'not_found')
      Object.assign(p, corpo)
      p.public_url = `http://localhost:5173/p/${p.slug}`
      return HttpResponse.json(p)
    }),

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
    rota('post', '/auth/password', ({ corpo }) => {
      if (corpo.current_password !== SENHA) return erro(401, 'invalid_credentials')
      if (st.totpLigado) {
        const codigo = String(corpo.totp_code ?? '')
        const recuperacao = codigo === RECUPERACAO_OK && !st.recuperacaoGasta.has(codigo)
        if (codigo !== TOTP_OK && !recuperacao) return erro(401, 'invalid_credentials')
        if (recuperacao) st.recuperacaoGasta.add(codigo)
        if (recuperacao && st.trocaFalhaDepoisDoCodigo) return erro(401, 'unauthorized')
      }
      const n = [...String(corpo.new_password)].length
      if (n < 15) return erro(400, 'validation_failed', [{ field: 'new_password', reason: 'too_short' }])
      st.sessoes = st.sessoes.filter((s) => s.current)
      // Task 11 do Plano 5: trocar a senha apaga o cadastro de 2FA pendente
      // e invalida os desafios de login em aberto.
      st.totpPendente = false
      st.desafioValido = false
      return new HttpResponse(null, { status: 204 })
    }),
    rota('post', '/auth/totp/enroll', ({ corpo }) => {
      if (corpo.password !== SENHA) return erro(401, 'invalid_credentials')
      if (st.totpLigado) return erro(403, 'forbidden')
      st.totpPendente = true
      return HttpResponse.json({ otpauth_uri: 'otpauth://totp/Vigilume:dono%40exemplo.com?secret=JBSWY3DPEHPK3PXP&issuer=Vigilume' })
    }),
    rota('post', '/auth/totp/confirm', ({ corpo }) => {
      // totphandlers.go: com o 2FA já ligado, 403 forbidden, como o enroll.
      if (st.totpLigado) return erro(403, 'forbidden')
      if (!st.totpPendente || corpo.code !== TOTP_OK) return erro(400, 'validation_failed')
      st.totpLigado = true
      st.totpPendente = false
      return HttpResponse.json({ recovery_codes: ['AAAABBBBCCCCDDDD', 'EEEEFFFFGGGGHHHH'] })
    }),
    rota('post', '/auth/totp/disable', ({ corpo }) => {
      // D24 do Plano 5: senha ou código errado é invalid_credentials;
      // unauthorized fica só para o token recusado.
      if (corpo.password !== SENHA || (corpo.code !== TOTP_OK && corpo.code !== RECUPERACAO_OK)) {
        return erro(401, 'invalid_credentials')
      }
      st.totpLigado = false
      // O disable apaga a troca pendente (spec do Plano 9 §3.2).
      st.troca = null
      return new HttpResponse(null, { status: 204 })
    }),
    // Spec do Plano 9 §3.2: as provas do disable (senha e código do app ou
    // de recuperação). 2FA desligado é 403; prova errada é
    // invalid_credentials (D24 do Plano 5). O código de recuperação fica
    // gasto mesmo que a troca não seja confirmada.
    rota('post', '/auth/totp/rotate', ({ corpo }) => {
      if (!st.totpLigado) return erro(403, 'forbidden')
      const codigo = String(corpo.code ?? corpo.recovery_code ?? '')
      const recuperacao = codigo === RECUPERACAO_OK && !st.recuperacaoGasta.has(codigo)
      if (corpo.password !== SENHA || (codigo !== TOTP_OK && !recuperacao)) return erro(401, 'invalid_credentials')
      if (recuperacao) st.recuperacaoGasta.add(codigo)
      // Outro rotate substitui o pendente, e o rotation muda com ele.
      st.troca = { rotation: rotacao(++st.rotacoes) }
      return HttpResponse.json({
        otpauth_uri: `otpauth://totp/Vigilume:dono%40exemplo.com?secret=${SEGREDO_NOVO}&issuer=Vigilume`,
        rotation: st.troca.rotation,
      })
    }),
    // Sem troca pendente, ou com o rotation de um pendente que outro rotate
    // já substituiu (A7 do Plano 10), é 403, sem contar falha; código errado
    // é 401 e o pendente fica. O sucesso encerra as OUTRAS sessões e entrega
    // 10 códigos novos.
    rota('post', '/auth/totp/rotate/confirm', ({ corpo }) => {
      if (!st.troca || corpo.rotation !== st.troca.rotation) return erro(403, 'forbidden')
      if (corpo.code !== TOTP_NOVO) return erro(401, 'invalid_credentials')
      st.troca = null
      st.sessoes = st.sessoes.filter((s) => s.current)
      return HttpResponse.json({ recovery_codes: CODIGOS_NOVOS })
    }),
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
