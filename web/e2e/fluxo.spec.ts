import type { APIRequestContext, APIResponse, Page, Route } from '@playwright/test'
import { controlePositivo, expect, test } from './fixtures'

// O alvo que o teste cadastra. Um passo que falha no meio deixaria o alvo
// para trás, e contra uma pilha externa eles se acumulariam até o teto de
// 100 alvos (reconciliação, item 4). O afterEach o procura pelo nome, pela
// API, e o apaga; no caminho feliz o próprio teste já o apagou (passo 7), e
// a busca não acha nada.
let nomeDoAlvo = ''

// Os limitadores (ver abaixo) respondem 429 sem Retry-After. A limpeza roda
// depois do fluxo, que gastou os baldes: um 429 aqui é o limite, não o
// produto. Espera com recuo limitado (2+4+8+16 s = 30 s, o bastante para o
// balde do login, 1 ficha a cada 6 s, e o por usuário, 1 por segundo) e tenta
// de novo; o afterEach tem prazo próprio de 60 s. Qualquer outro status volta
// na hora e cai na asserção de quem chamou: um 500 não vira nova tentativa.
const RECUOS_DO_429_MS = [2_000, 4_000, 8_000, 16_000]

async function comRecuo(oque: string, fazer: () => Promise<APIResponse>): Promise<APIResponse> {
  for (let i = 0; ; i++) {
    const r = await fazer()
    if (r.status() !== 429) return r
    const espera = RECUOS_DO_429_MS[i]
    if (espera === undefined) {
      // Esgotado: falha com o motivo claro, sem se passar por outro erro.
      throw new Error(
        `limpeza do E2E: ${oque} ainda em 429 depois de ${RECUOS_DO_429_MS.length} novas tentativas ` +
          `(${RECUOS_DO_429_MS.reduce((a, b) => a + b, 0) / 1000} s de recuo); o alvo "${nomeDoAlvoEmLimpeza}" pode ter ficado para trás`,
      )
    }
    console.log(`limpeza do E2E: ${oque} deu 429; nova tentativa em ${espera / 1000} s`)
    await new Promise((ok) => setTimeout(ok, espera))
  }
}

let nomeDoAlvoEmLimpeza = ''

// Um login só: o token dele serve à lista e a todos os DELETE (o balde do
// login é o mais apertado, 5 de burst por IP).
async function apagarPeloNome(request: APIRequestContext, email: string, senha: string, nome: string) {
  nomeDoAlvoEmLimpeza = nome
  const mutacao = { 'X-Requested-With': 'stillon' }
  const login = await comRecuo('o login', () =>
    request.post('/api/v1/auth/login', { headers: mutacao, data: { email, password: senha } }),
  )
  expect(login.status(), 'login da limpeza').toBe(200)
  const { access_token: token } = (await login.json()) as { access_token: string }
  const autorizado = { Authorization: `Bearer ${token}` }
  try {
    const lista = await comRecuo('a lista de alvos', () => request.get('/api/v1/targets?limit=100', { headers: autorizado }))
    expect(lista.status(), 'lista da limpeza').toBe(200)
    const { items } = (await lista.json()) as { items: { id: string; name: string }[] }
    const sobras = items.filter((a) => a.name === nome)
    for (const a of sobras) {
      const r = await comRecuo(`o DELETE de ${a.id}`, () =>
        request.delete(`/api/v1/targets/${a.id}`, { headers: { ...autorizado, ...mutacao } }),
      )
      expect([204, 404], `apagar ${a.id}`).toContain(r.status())
    }
    console.log(`limpeza do E2E: ${sobras.length} alvo(s) "${nome}" apagado(s)`)
  } finally {
    // O cookie de refresh do login da limpeza ficou no contexto do request:
    // o logout encerra essa sessão também. Com recuo: um 429 aqui deixaria a
    // sessão viva até expirar.
    await comRecuo('o logout', () => request.post('/api/v1/auth/logout', { headers: mutacao })).catch((e: unknown) =>
      console.log(String(e)),
    )
  }
}

// Os limitadores de internal/httpapi/routes.go são baldes que se enchem com
// o tempo, e as specs anteriores (abas, a11y) gastam a mesma conta e o mesmo
// IP: sem pausa, o fluxo às vezes pegava 429 no detalhe (visto na onda final).
// - por usuário (RateLimitUser 60/min, burst 20): 20 s para encher de novo;
// - por IP em /api (RateLimit 120/min, burst 30): 15 s;
// - login (RateLimit 10/min, burst 5 por IP): 5 logins em 30 s; o fluxo faz
//   dois, mais um na limpeza.
// A pausa é do próprio fluxo (beforeAll), e não depende da ordem das specs.
// 21 s cobre o balde mais lento com folga.
const RECARGA_DOS_BALDES_MS = 21_000

test.beforeAll(async () => {
  test.setTimeout(RECARGA_DOS_BALDES_MS + 10_000)
  await new Promise((ok) => setTimeout(ok, RECARGA_DOS_BALDES_MS))
})

test.afterEach(async ({ request, credenciais }) => {
  if (!nomeDoAlvo) return
  const nome = nomeDoAlvo
  nomeDoAlvo = ''
  await apagarPeloNome(request, credenciais.email, credenciais.senha, nome)
})

// O fluxo crítico da spec §14: login → cadastrar alvo → ver status. Mais o
// que só o navegador de verdade prova: F5 com o cookie, a CSP de produção, o
// chunk do Recharts desenhando a linha, e o token fora do storage.
test('login, cadastro de alvo, status, F5, detalhe, apagar, encerrar a sessão e sair', async ({ page, credenciais }, info) => {
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    nomeDoAlvo = `E2E ${Date.now()}`

    // 1. Sem sessão: o splash e depois o login; nunca o painel piscando.
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
    await controlePositivo(page, 'o login')

    // 2. Login só com o teclado, do topo da página: Tab até o e-mail, Tab até
    //    a senha, Enter.
    await page.keyboard.press('Tab')
    await expect(page.getByLabel('E-mail')).toBeFocused()
    await page.keyboard.type(credenciais.email)
    await page.keyboard.press('Tab')
    await expect(page.getByLabel('Senha')).toBeFocused()
    await page.keyboard.type(credenciais.senha)
    await page.keyboard.press('Enter')
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()

    // 3. Cadastrar. O alvo .invalid (RFC 2606) passa pela validação de
    //    cadastro, que não resolve DNS, e nunca resolve: sem rede externa.
    await page.getByRole('link', { name: 'Cadastrar alvo' }).click()
    await page.getByLabel('Nome').fill(nomeDoAlvo)
    await page.getByLabel('URL').fill('https://stillon-e2e.invalid/')
    await page.getByRole('button', { name: 'Cadastrar' }).click()
    await expect(page.getByRole('heading', { name: nomeDoAlvo })).toBeVisible()

    // 4. O dashboard mostra o alvo (reconciliação, item 4: sem depender do
    //    estado, que um DNS que sequestra NXDOMAIN poderia mudar). A primeira
    //    sonda sai em ~1 s; o próprio poll do dashboard (15 s) traz o estado
    //    classificado, sem recarregar a página (recarregar em laço gastaria o
    //    burst do limite por usuário).
    await page.getByRole('link', { name: 'Painel' }).click()
    const cartao = page.getByRole('listitem').filter({ has: page.getByRole('link', { name: nomeDoAlvo }) })
    await expect(cartao).toBeVisible()
    await expect(cartao.getByText(/^(No ar|Degradado|Fora do ar)$/)).toBeVisible({ timeout: 30_000 })

    // 5. F5: continua dentro, pelo cookie de refresh.
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()

    // 6. O detalhe carrega o chunk do Recharts. O alvo .invalid só tem checks
    //    "fora do ar", que viram marcas na base do gráfico; a marca tem cor
    //    computada (variável CSS do index.css). O dashboard já mostrou um
    //    check: a série tem pontos na primeira carga.
    await page.getByRole('link', { name: nomeDoAlvo }).click()
    await expect(page.getByText(/no ar nesta janela/)).toBeVisible()
    const marca = page.getByTestId('grafico-latencia').locator('.recharts-symbols').first()
    await expect(marca).toBeAttached()
    expect(await marca.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^rgb/)

    // 7. Um diálogo (Radix + react-remove-scroll, que injeta <style>) trava a
    //    rolagem: o nonce funcionou. E o alvo sai: o teto é de 100 alvos, e
    //    o E2E roda de novo contra a mesma pilha externa (reconciliação, item 4).
    await page.getByRole('button', { name: 'Apagar' }).click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).overflow)).toBe('hidden')
    await page.getByRole('button', { name: 'Apagar alvo' }).click()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
    await expect(page.getByRole('link', { name: nomeDoAlvo })).toHaveCount(0)

    // 8. Nada do token no storage; o cookie de refresh é invisível ao JS. (Duas
    //    guardas: o HttpOnly é do servidor — este teste prova a propriedade.)
    const armazenado = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))
    expect(armazenado).not.toMatch(/eyJ/)
    expect(await page.evaluate(() => document.cookie)).not.toContain('stillon_refresh')

    // 9. Zero violações de CSP no caminho todo, inclusive antes dos F5.
    await controlePositivo(page, 'o caminho até o passo 9')

    // 10. Encerrar a própria sessão em Configurações: o 204 apagou o cookie, e
    //     o F5 não volta (nota do Plano 5 §12).
    await page.getByRole('link', { name: 'Configurações' }).click()
    const atual = page.getByRole('listitem').filter({ hasText: 'Esta sessão' })
    await atual.getByRole('button', { name: 'Encerrar' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Encerrar' }).click()
    await expect(page.getByText('Você encerrou esta sessão.')).toBeVisible()
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()

    // 11. Entrar de novo (o ?next= volta a Configurações), sair; F5 continua fora.
    await page.getByLabel('E-mail').fill(credenciais.email)
    await page.getByLabel('Senha').fill(credenciais.senha)
    await page.getByRole('button', { name: 'Entrar' }).click()
    await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()
    await page.getByRole('button', { name: 'Sair' }).click()
    await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
    await controlePositivo(page, 'o fim do fluxo')
  } finally {
    await encerrar()
  }
})

// Uma ficha por segundo é o contrato público de RateLimitUser(60, 20).
// Reservar os turnos antes de aguardar evita que GETs paralelos formem um lote.
// Auth/refresh seguem nativos; nenhuma resposta, mutação ou falha é repetida.
async function cadenciarRecursos(page: Page, prazo: number) {
  const padrao = /\/api\/v1\/(?!auth(?:\/|$))/
  let proximo = 0
  let fila = Promise.resolve()
  let encerrada = false
  const esperas = new Map<ReturnType<typeof setTimeout>, () => void>()
  const pendentes = new Set<Promise<void>>()
  const erros: unknown[] = []

  async function executar(route: Route) {
    const agora = performance.now()
    const turno = Math.max(agora, proximo)
    proximo = turno + 1_000
    if (!encerrada && turno >= prazo) {
      await route.abort('timedout')
      throw new Error('cadência do E2E: recurso não cabe no prazo original do teste')
    }
    while (performance.now() < turno) {
      if (encerrada) break
      await new Promise<void>((ok) => {
        const timer = setTimeout(() => {
          esperas.delete(timer)
          ok()
        }, Math.ceil(turno - performance.now()))
        esperas.set(timer, ok)
      })
    }
    if (!encerrada && performance.now() >= prazo) {
      await route.abort('timedout')
      throw new Error('cadência do E2E: prazo original do teste esgotado')
    }
    try {
      if (encerrada) await route.abort('aborted')
      else await route.continue()
    } catch (erro) {
      // F5/navegação pode cancelar uma requisição durante seu turno. Somente
      // o cancelamento confirmado pelo navegador é esperado; o resto falha.
      if (!route.request().failure()) throw erro
    } finally {
      // Um timer atrasado não libera os turnos seguintes em lote.
      proximo = Math.max(proximo, performance.now() + 1_000)
    }
  }

  async function encaminhar(route: Route) {
    // A cadeia reserva a ordem antes de qualquer await e despacha um recurso
    // por vez, inclusive se o event loop atrasar um timer. Erros são guardados
    // abaixo e tornam o teste vermelho ao encerrar; não viram novas tentativas.
    const trabalho = fila.then(() => executar(route))
    fila = trabalho.then(() => {}, () => {})
    pendentes.add(trabalho)
    try { await trabalho } catch (erro) { erros.push(erro) }
    finally { pendentes.delete(trabalho) }
  }
  await page.route(padrao, encaminhar)
  return async () => {
    encerrada = true
    for (const [timer, ok] of esperas) {
      clearTimeout(timer)
      ok()
    }
    esperas.clear()
    // Remover a rota antes de concluir os handlers faria o navegador tratar
    // pedidos ainda enfileirados; primeiro conclui os abortos, sem timers.
    await Promise.allSettled(pendentes)
    await page.unroute(padrao, encaminhar)
    if (erros.length) throw erros[0]
  }
}

test('a cadência das rotas protegidas preserva o 401 real e evita disparo em lote', async ({ page }, info) => {
  await page.goto('/login')
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const inicio = performance.now()
    const status = await page.evaluate(async () => Promise.all(
      ['/api/v1/me', '/api/v1/sessions', '/api/v1/channels'].map(async (path) => (await fetch(path)).status),
    ))
    expect(status).toEqual([401, 401, 401])
    // Três recursos na cadência pública de 60/min precisam de dois segundos
    // entre primeiro e terceiro. O lote nativo terminava em milissegundos.
    expect(performance.now() - inicio).toBeGreaterThanOrEqual(2_000)
  } finally {
    await encerrar()
  }
})

test('encerrar a cadência cancela os recursos que ainda aguardam seu turno', async ({ page }, info) => {
  await page.goto('/login')
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  const primeira = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/me')
  const resultados = page.evaluate(async () => Promise.all(
    ['/api/v1/me', '/api/v1/sessions', '/api/v1/channels'].map(async (path) => {
      try { return (await fetch(path)).status } catch { return 'cancelada' }
    }),
  ))
  try {
    expect((await primeira).status()).toBe(401)
  } finally {
    await encerrar()
  }
  expect(await resultados).toEqual([401, 'cancelada', 'cancelada'])
})

test('a cadência recusa recursos que não cabem no prazo e expõe a falha', async ({ page }) => {
  await page.goto('/login')
  const encerrar = await cadenciarRecursos(page, performance.now() - 1)
  try {
    const resultados = await page.evaluate(async () => Promise.all(
      ['/api/v1/me', '/api/v1/sessions', '/api/v1/channels'].map(async (path) => {
        try { return (await fetch(path)).status } catch { return 'cancelada' }
      }),
    ))
    expect(resultados).toEqual(['cancelada', 'cancelada', 'cancelada'])
  } finally {
    await expect(encerrar()).rejects.toThrow('recurso não cabe no prazo original do teste')
  }
})
