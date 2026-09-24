import type { APIRequestContext } from '@playwright/test'
import { controlePositivo, expect, test, violacoes } from './fixtures'

// O alvo que o teste cadastra. Um passo que falha no meio deixaria o alvo
// para trás, e contra uma pilha externa eles se acumulariam até o teto de
// 100 alvos (reconciliação, item 4). O afterEach o procura pelo nome, pela
// API, e o apaga; no caminho feliz o próprio teste já o apagou (passo 7), e
// a busca não acha nada.
let nomeDoAlvo = ''

async function apagarPeloNome(request: APIRequestContext, email: string, senha: string, nome: string) {
  const mutacao = { 'X-Requested-With': 'vigilume' }
  const login = await request.post('/api/v1/auth/login', { headers: mutacao, data: { email, password: senha } })
  expect(login.ok(), 'login da limpeza').toBe(true)
  const { access_token: token } = (await login.json()) as { access_token: string }
  const autorizado = { Authorization: `Bearer ${token}` }
  try {
    const lista = await request.get('/api/v1/targets?limit=100', { headers: autorizado })
    const { items } = (await lista.json()) as { items: { id: string; name: string }[] }
    const sobras = items.filter((a) => a.name === nome)
    for (const a of sobras) {
      const r = await request.delete(`/api/v1/targets/${a.id}`, { headers: { ...autorizado, ...mutacao } })
      expect([204, 404], `apagar ${a.id}`).toContain(r.status())
    }
    console.log(`limpeza do E2E: ${sobras.length} alvo(s) "${nome}" apagado(s)`)
  } finally {
    // O cookie de refresh do login da limpeza ficou no contexto do request:
    // o logout encerra essa sessão também.
    await request.post('/api/v1/auth/logout', { headers: mutacao })
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
test('login, cadastro de alvo, status, F5, detalhe, apagar, encerrar a sessão e sair', async ({ page, credenciais }) => {
  nomeDoAlvo = `E2E ${Date.now()}`

  // 1. Sem sessão: o splash e depois o login; nunca o painel piscando.
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  await controlePositivo(page)

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
  await page.getByLabel('URL').fill('https://vigilume-e2e.invalid/')
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
  expect(await page.evaluate(() => document.cookie)).not.toContain('vigilume_refresh')

  // 9. Zero violações de CSP no caminho todo, inclusive antes dos F5.
  expect(violacoes(page)).toEqual([])

  // 10. Encerrar a própria sessão em Configurações: o 204 apagou o cookie, e
  //     o F5 não volta (nota do Plano 5 §12).
  await page.getByRole('link', { name: 'Configurações' }).click()
  const atual = page.getByRole('listitem').filter({ hasText: 'Esta sessão' })
  await atual.getByRole('button', { name: 'Encerrar' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Encerrar' }).click()
  await expect(page.getByText('Você encerrou esta sessão.')).toBeVisible()
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()

  // 11. Entrar de novo (o ?next= volta a Configurações), sair; F5 continua fora.
  await page.getByLabel('E-mail').fill(credenciais.email)
  await page.getByLabel('Senha').fill(credenciais.senha)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()
  await page.getByRole('button', { name: 'Sair' }).click()
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  await expect.poll(() => violacoes(page)).toEqual([])
})
