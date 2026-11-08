import type { APIRequestContext, APIResponse } from '@playwright/test'
import { cadenciarRecursos } from './cadencia'
import { controlePositivo, expect, test } from './fixtures'

// Um clique não depende da cadência nem do poll: ele só espera o elemento. Sem
// este prazo, um elemento que nunca aparece consumia o tempo inteiro do teste
// e falhava sem dizer o passo.
test.use({ actionTimeout: 15_000 })

// O que o teste cadastra. Um passo que falha no meio deixaria o monitor para
// trás, e contra uma pilha externa eles se acumulariam até o teto de 100
// (reconciliação, item 4). O afterEach o procura pelo nome, pela API, e o
// apaga; no caminho feliz o próprio teste já o apagou, e a busca não acha nada.
// São vários nomes enquanto o teste não souber qual deles o servidor guardou.
type Sobra = { rota: 'targets' | 'heartbeats'; nomes: string[] }
let sobra: Sobra | null = null

// Os limitadores (ver abaixo) respondem 429; o Retry-After não é lido aqui. A limpeza roda
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
          `(${RECUOS_DO_429_MS.reduce((a, b) => a + b, 0) / 1000} s de recuo); "${nomeEmLimpeza}" pode ter ficado para trás`,
      )
    }
    console.log(`limpeza do E2E: ${oque} deu 429; nova tentativa em ${espera / 1000} s`)
    await new Promise((ok) => setTimeout(ok, espera))
  }
}

let nomeEmLimpeza = ''

// Um login só: o token dele serve à lista e a todos os DELETE (o balde do
// login é o mais apertado, 5 de burst por IP).
async function apagarPeloNome(request: APIRequestContext, email: string, senha: string, { rota, nomes }: Sobra) {
  nomeEmLimpeza = nomes.join('" ou "')
  const mutacao = { 'X-Requested-With': 'stillon' }
  const login = await comRecuo('o login', () =>
    request.post('/api/v1/auth/login', { headers: mutacao, data: { email, password: senha } }),
  )
  expect(login.status(), 'login da limpeza').toBe(200)
  const { access_token: token } = (await login.json()) as { access_token: string }
  const autorizado = { Authorization: `Bearer ${token}` }
  try {
    const lista = await comRecuo(`a lista de ${rota}`, () => request.get(`/api/v1/${rota}?limit=100`, { headers: autorizado }))
    expect(lista.status(), 'lista da limpeza').toBe(200)
    const { items } = (await lista.json()) as { items: { id: string; name: string }[] }
    const sobras = items.filter((a) => nomes.includes(a.name))
    for (const a of sobras) {
      const r = await comRecuo(`o DELETE de ${a.id}`, () =>
        request.delete(`/api/v1/${rota}/${a.id}`, { headers: { ...autorizado, ...mutacao } }),
      )
      expect([204, 404], `apagar ${a.id}`).toContain(r.status())
    }
    console.log(`limpeza do E2E: ${sobras.length} item(ns) "${nomeEmLimpeza}" apagado(s) de ${rota}`)
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
  if (!sobra) return
  const resto = sobra
  sobra = null
  await apagarPeloNome(request, credenciais.email, credenciais.senha, resto)
})

// O fluxo crítico da spec §14: login → cadastrar alvo → ver status. Mais o
// que só o navegador de verdade prova: F5 com o cookie, a CSP de produção, o
// chunk do Recharts desenhando a linha, e o token fora do storage.
test('login, cadastro de alvo, status, F5, detalhe, apagar, encerrar a sessão e sair', async ({ page, credenciais }, info) => {
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const nomeDoAlvo = `E2E ${Date.now()}`
    sobra = { rota: 'targets', nomes: [nomeDoAlvo] }

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
    //    O título primeiro: a navegação é uma transição, e até ela terminar o
    //    "Nome" casaria com a busca do painel ("Buscar por nome ou tag").
    await page.getByRole('link', { name: 'Cadastrar alvo' }).click()
    await expect(page.getByRole('heading', { name: 'Novo alvo' })).toBeVisible()
    await page.getByLabel('Nome', { exact: true }).fill(nomeDoAlvo)
    await page.getByLabel('URL').fill('https://stillon-e2e.invalid/')
    await page.getByLabel('Tags').fill('Fluxo-E2E, fluxo-e2e')
    await page.getByRole('button', { name: 'Cadastrar' }).click()
    await expect(page.getByRole('heading', { name: nomeDoAlvo })).toBeVisible()

    // 4. O dashboard mostra o alvo (reconciliação, item 4: sem depender do
    //    estado, que um DNS que sequestra NXDOMAIN poderia mudar). A primeira
    //    sonda sai em ~1 s; o próprio poll do dashboard (15 s) traz o estado
    //    classificado, sem recarregar a página (recarregar em laço gastaria o
    //    burst do limite por usuário).
    // O alvo aparece primeiro como "Sem dados" e, classificado, passa a
    // "Fora do ar" (o .invalid) ou a "No ar" (um DNS que sequestra NXDOMAIN).
    // exact: a linha tem também o "Ver incidente de <nome>".
    // A aba da navegação: o detalhe também tem o "Voltar ao painel".
    await page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: 'Painel' }).click()
    await expect(page.getByRole('link', { name: nomeDoAlvo, exact: true })).toBeVisible()
    const classificado = page
      .getByRole('table', { name: /^Monitores/ })
      .getByRole('row', { name: new RegExp(`(Fora do ar|No ar).*${nomeDoAlvo}`) })
    await expect(classificado).toBeVisible({ timeout: 30_000 })

    // O filtro contra a API de verdade: a tag normalizada aparece na barra, e
    // filtrar por ela mantém o alvo na tabela.
    await page.getByRole('group', { name: 'Tags' }).getByRole('button', { name: /^fluxo-e2e/ }).click()
    await expect(page).toHaveURL(/tag=fluxo-e2e/)
    await expect(classificado).toBeVisible()
    await page.getByRole('button', { name: 'Limpar filtros' }).click()
    await expect(page).not.toHaveURL(/tag=/)

    // 5. F5: continua dentro, pelo cookie de refresh.
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()

    // 6. O detalhe carrega o chunk do Recharts. O alvo .invalid só tem checks
    //    "fora do ar", que viram faixas hachuradas; o traço do padrão tem
    //    cor computada (variável CSS do index.css). O dashboard já mostrou
    //    um check: a série tem pontos na primeira carga.
    await page.getByRole('link', { name: nomeDoAlvo, exact: true }).click()
    await expect(page.getByText(/no ar nesta janela/)).toBeVisible()
    const faixa = page.getByTestId('grafico-latencia').locator('.recharts-reference-area-rect').first()
    await expect(faixa).toBeAttached()
    const padrao = (await faixa.getAttribute('fill'))?.match(/^url\(#(.+)\)$/)?.[1]
    expect(padrao).toBeTruthy()
    expect(await page.locator(`pattern[id="${padrao}"] path`).evaluate((e) => getComputedStyle(e).stroke)).toMatch(
      /^(rgb|oklch)\(/,
    )

    // 7. Um diálogo (Radix + react-remove-scroll, que injeta <style>) trava a
    //    rolagem: o nonce funcionou. E o alvo sai: o teto é de 100 alvos, e
    //    o E2E roda de novo contra a mesma pilha externa (reconciliação, item 4).
    await page.getByRole('button', { name: 'Apagar' }).click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    await expect.poll(() => page.evaluate(() => getComputedStyle(document.body).overflow)).toBe('hidden')
    await page.getByRole('button', { name: 'Apagar alvo' }).click()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
    await expect(page.getByRole('link', { name: nomeDoAlvo })).toHaveCount(0)

    // 8. Nada do token no storage, e o cookie de refresh é invisível ao JS. O
    //    atributo vem do jar do navegador: o document.cookie desta página nunca o
    //    traria, com HttpOnly ou sem, porque o Path dele é só o da autenticação.
    const armazenado = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }))
    expect(armazenado).not.toMatch(/eyJ/)
    const cookies = await page.context().cookies()
    expect(cookies.find((c) => c.name === 'stillon_refresh')).toMatchObject({
      httpOnly: true,
      sameSite: 'Strict',
      path: '/api/v1/auth',
    })

    // 9. Zero violações de CSP no caminho todo, inclusive antes dos F5.
    await controlePositivo(page, 'o caminho até o passo 9')

    // 10. Encerrar a própria sessão em Configurações: o 204 apagou o cookie, e
    //     o F5 não volta (nota do Plano 5 §12).
    await page.getByRole('link', { name: 'Conta', exact: true }).click()
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

// A jornada da tarefa, contra a pilha real: o sinal sai por HTTP, sem cookie,
// como sai de um cron, e o painel só descobre pelo próprio poll (15 a 30 s,
// mais os turnos da cadência e os 5 s do avaliador). Daí o prazo de cada
// espera e o do teste. O atraso por relógio fica de fora: o menor prazo real é
// de 90 s (60 de período, 30 de tolerância), e a pilha não tem relógio
// controlável; quem o cobre são os testes Go do avaliador.
const ESPERA_DO_POLL_MS = 45_000
const PRAZO_DA_JORNADA_MS = 300_000
const URL_DE_PING = /\/ping\/[\w-]{43}$/
const TAREFA_PEDE_ATENCAO = /\buma tarefa pedem? atenção\./i
// Regex: o número soma os incidentes de alvo da pilha aos da tarefa.
const ABA_COM_ABERTOS = /^Incidentes, \d+\+? abertos?$/
// Sem tarefa na manchete, a frase depende dos alvos da pilha: nenhum na própria,
// e o example.com do seed no CI, normalmente no ar.
const MANCHETE_SEM_TAREFA = /^(Todos os monitores estão pausados|Tudo no ar|Um serviço pede atenção)\.$/

test('heartbeat: cadastro, URL, sinais reais, incidente, pausa, retomada, rotação e apagar', async ({ page, context, request, credenciais }, info) => {
  test.setTimeout(PRAZO_DA_JORNADA_MS)
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const nome = `Tarefa E2E ${Date.now()}`
    sobra = { rota: 'heartbeats', nomes: [nome] }
    const noPoll = { timeout: ESPERA_DO_POLL_MS }
    const irAoPainel = () => page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: 'Painel' }).click()
    const selo = (texto: string) => page.getByText(texto, { exact: true })
    const log = page.getByRole('region', { name: 'Log de eventos' }).getByRole('listitem')
    const incidentes = page.getByRole('region', { name: 'Incidentes' }).getByRole('listitem')
    const linha = (estado: string) =>
      page.getByRole('table', { name: /^Monitores/ }).getByRole('row', { name: new RegExp(`${estado}.*${nome}`) })
    // O título da asserção não leva a URL: ela é a credencial, mesmo a de teste.
    const postar = async (destino: string, status: number) =>
      expect((await request.post(destino)).status(), `POST em ${new URL(destino).pathname.replace(/[\w-]{43}/, '…')}`).toBe(status)

    // 1. Entrar.
    await page.goto('/')
    await page.getByLabel('E-mail').fill(credenciais.email)
    await page.getByLabel('Senha').fill(credenciais.senha)
    await page.getByRole('button', { name: 'Entrar' }).click()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()

    // 2. Cadastrar: período e tolerância padrão, com o prazo longe do teste.
    await page.getByRole('link', { name: 'Cadastrar heartbeat' }).click()
    await expect(page.getByRole('heading', { name: 'Novo heartbeat' })).toBeVisible()
    await page.getByLabel('Nome', { exact: true }).fill(nome)
    await page.getByRole('button', { name: 'Cadastrar', exact: true }).click()

    // 3. Copiar a URL mostrada uma vez, pelo botão: vale o que chega à área de
    //    transferência, que é o que vai para a tarefa.
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    const copiarUrl = async () => {
      const dialogo = page.getByRole('dialog', { name: 'Guarde a URL de ping' })
      // Sem isto, um POST recusado só apareceria no fim do prazo do teste: o
      // clique abaixo espera sem limite próprio.
      await expect(dialogo).toBeVisible(noPoll)
      const copiar = dialogo.getByRole('button', { name: 'Copiar a URL de ping' })
      await copiar.click()
      await expect(copiar).toHaveText('Copiado')
      const copiada = await page.evaluate(() => navigator.clipboard.readText())
      expect(URL_DE_PING.test(copiada), 'formato da URL de ping').toBe(true)
      await dialogo.getByRole('button', { name: 'Já guardei' }).click()
      await expect(dialogo).toHaveCount(0)
      return copiada
    }
    const url = await copiarUrl()

    // Só POST (D6): um GET não conta, e a tarefa segue aguardando.
    await expect(page.getByRole('heading', { name: nome })).toBeVisible()
    await expect(selo('Aguardando')).toBeVisible()
    expect((await request.get(url)).status()).toBe(405)
    await expect(page.getByText('Nenhum sinal recebido.')).toBeVisible()

    // 4. Início e sucesso de verdade: estado e log mudam sem recarregar.
    await postar(`${url}/start`, 200)
    await postar(url, 200)
    await expect(selo('Em dia')).toBeVisible(noPoll)
    await expect(log).toHaveCount(2, noPoll)
    await expect(log.nth(0)).toContainText('Sucesso')
    await expect(log.nth(0)).toContainText(/levou \d+ (ms|s)/)
    await expect(log.nth(1)).toContainText('Início')
    await expect(page.getByText('Nenhum incidente.')).toBeVisible()

    // 5. Falha: o selo vem da leitura, e o incidente, do avaliador.
    await postar(`${url}/fail`, 200)
    await expect(selo('Falhando')).toBeVisible(noPoll)
    await expect(incidentes.filter({ hasText: 'A tarefa avisou falha' }).getByText('Em andamento')).toBeVisible(noPoll)
    await expect(log).toHaveCount(3, noPoll)
    await expect(log.nth(0)).toContainText('Falha')

    // A manchete fala da tarefa. Regex: a pilha do CI tem os alvos do seed, e
    // a frase pode ser "Um serviço e uma tarefa pedem atenção."
    await irAoPainel()
    await expect(page.getByText(TAREFA_PEDE_ATENCAO)).toBeVisible(noPoll)
    await expect(page.getByText(/Tarefas: 1 com falha\./)).toBeVisible()
    await expect(linha('Falhando')).toBeVisible()
    await page.getByRole('link', { name: nome, exact: true }).click()

    // 6. Pausar não encerra o incidente (D13): a manchete segue pedindo atenção.
    await page.getByRole('button', { name: 'Pausar' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Pausar heartbeat' }).click()
    await expect(selo('Pausado')).toBeVisible(noPoll)
    //    A linha vem primeiro: ela prova que a lista já é a nova, e a manchete
    //    nasce da mesma lista.
    await irAoPainel()
    await expect(linha('Pausado')).toBeVisible(noPoll)
    await expect(page.getByText(TAREFA_PEDE_ATENCAO)).toBeVisible()
    await expect(page.getByText(/Tarefas: 1 pausada\./)).toBeVisible()
    await expect(page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: ABA_COM_ABERTOS })).toBeVisible(noPoll)
    await page.getByRole('link', { name: nome, exact: true }).click()

    // 7. Retomar zera a falha, e o avaliador fecha o incidente.
    await page.getByRole('button', { name: 'Retomar' }).click()
    await expect(selo('Em dia')).toBeVisible(noPoll)
    await expect(incidentes.getByText('Resolvido')).toBeVisible(noPoll)
    await expect(incidentes.getByText('Em andamento')).toHaveCount(0)

    // 8. Rotacionar: a URL antiga morre na hora, e a nova fala com o mesmo
    //    heartbeat (o sucesso entra no mesmo log).
    await page.getByRole('button', { name: 'Gerar nova URL' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Gerar nova URL' }).click()
    const urlNova = await copiarUrl()
    expect(urlNova === url, 'a rotação troca a URL').toBe(false)
    await postar(url, 404)
    await postar(`${url}/start`, 404)
    await postar(`${url}/fail`, 404)
    await postar(urlNova, 200)
    await expect(log).toHaveCount(4, noPoll)
    await expect(log.nth(0)).toContainText('Sucesso')

    // A credencial não ficou em lugar nenhum do navegador (spec da tela, T8).
    const guardado = await page.evaluate(() => JSON.stringify([localStorage, sessionStorage, history.state, document.body.innerText]))
    for (const u of [url, urlNova]) expect(guardado.includes(u.slice(u.lastIndexOf('/') + 1)), 'token no navegador').toBe(false)

    // 9. Pausada sem incidente, a tarefa sai da manchete.
    await page.getByRole('button', { name: 'Pausar' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Pausar heartbeat' }).click()
    await expect(selo('Pausado')).toBeVisible(noPoll)
    await irAoPainel()
    await expect(linha('Pausado')).toBeVisible(noPoll)
    await expect(page.getByRole('heading', { level: 2, name: MANCHETE_SEM_TAREFA })).toBeVisible()
    await expect(page.getByText(TAREFA_PEDE_ATENCAO)).toHaveCount(0)
    await page.getByRole('link', { name: nome, exact: true }).click()

    // 10. Apagar: some do painel, e a URL deixa de existir.
    await page.getByRole('button', { name: 'Apagar' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Apagar heartbeat' }).click()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible(noPoll)
    await expect(page.getByRole('link', { name: nome })).toHaveCount(0)
    await postar(urlNova, 404)
    sobra = null

    await controlePositivo(page, 'o fim da jornada do heartbeat')
  } finally {
    await encerrar()
  }
})

// O resto da tela da tarefa: editar sem perder a URL, reconhecer sem resolver e
// o sucesso que fecha o incidente reconhecido. A URL sai do texto do diálogo, e
// não da área de transferência, para a jornada valer fora do Chromium.
test('heartbeat: editar, reconhecer o incidente e o sucesso que o fecha', async ({ page, request, credenciais }, info) => {
  test.setTimeout(PRAZO_DA_JORNADA_MS)
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const nome = `Tarefa E2E ${Date.now()}`
    const nomeNovo = `${nome} editada`
    sobra = { rota: 'heartbeats', nomes: [nome] }
    const noPoll = { timeout: ESPERA_DO_POLL_MS }
    const selo = (texto: string) => page.getByText(texto, { exact: true })
    const log = page.getByRole('region', { name: 'Log de eventos' }).getByRole('listitem')
    const incidente = page.getByRole('region', { name: 'Incidentes' }).getByRole('listitem').filter({ hasText: 'A tarefa avisou falha' })
    const reconhecer = incidente.getByRole('button', { name: /^Reconhecer o incidente de / })
    const postar = async (destino: string) =>
      expect((await request.post(destino)).status(), `POST em ${new URL(destino).pathname.replace(/[\w-]{43}/, '…')}`).toBe(200)

    // 1. Entrar e cadastrar, com a URL lida do diálogo.
    await page.goto('/')
    await page.getByLabel('E-mail').fill(credenciais.email)
    await page.getByLabel('Senha').fill(credenciais.senha)
    await page.getByRole('button', { name: 'Entrar' }).click()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
    await page.getByRole('link', { name: 'Cadastrar heartbeat' }).click()
    await expect(page.getByRole('heading', { name: 'Novo heartbeat' })).toBeVisible()
    await page.getByLabel('Nome', { exact: true }).fill(nome)
    await page.getByRole('button', { name: 'Cadastrar', exact: true }).click()
    const dialogo = page.getByRole('dialog', { name: 'Guarde a URL de ping' })
    await expect(dialogo).toBeVisible(noPoll)
    const url = (await dialogo.locator('code').first().textContent()) ?? ''
    expect(URL_DE_PING.test(url), 'formato da URL de ping').toBe(true)
    await dialogo.getByRole('button', { name: 'Já guardei' }).click()
    await expect(page.getByRole('heading', { name: nome })).toBeVisible()

    // 2. Editar: o formulário abre com o que está gravado, e a tela volta com o
    //    que mudou. O F5 prova que veio do servidor, e não do cache da consulta.
    await page.getByRole('link', { name: 'Editar' }).click()
    await expect(page.getByRole('heading', { name: 'Editar heartbeat' })).toBeVisible()
    await expect(page.getByLabel('Nome', { exact: true })).toHaveValue(nome)
    await page.getByLabel('Nome', { exact: true }).fill(nomeNovo)
    await page.getByLabel('Tags').fill('Fluxo-E2E')
    await page.getByLabel('Descrição').fill('Copia o banco para o armazenamento frio.')
    await page.getByLabel('Período', { exact: true }).fill('2')
    await page.getByLabel('Unidade de período').selectOption('horas')
    // Do clique até o título, o teste não sabe se o PATCH entrou: uma falha nesse
    // trecho deixaria a tarefa com qualquer um dos dois nomes.
    sobra = { rota: 'heartbeats', nomes: [nome, nomeNovo] }
    await page.getByRole('button', { name: 'Salvar' }).click()
    await expect(page.getByRole('heading', { name: nomeNovo })).toBeVisible()
    sobra = { rota: 'heartbeats', nomes: [nomeNovo] }
    await page.reload()
    await expect(page.getByRole('heading', { name: nomeNovo })).toBeVisible()
    await expect(page.getByText('Copia o banco para o armazenamento frio.')).toBeVisible()
    await expect(page.getByText('fluxo-e2e', { exact: true })).toBeVisible()
    await expect(page.getByText('a cada 2 h', { exact: true })).toBeVisible()

    // 3. A edição não troca a URL: o sinal dela cai neste mesmo heartbeat.
    await postar(url)
    await expect(selo('Em dia')).toBeVisible(noPoll)
    await expect(log).toHaveCount(1, noPoll)

    // 4. Falha: o incidente abre pelo avaliador, à espera de reconhecimento.
    await postar(`${url}/fail`)
    await expect(selo('Falhando')).toBeVisible(noPoll)
    await expect(incidente.getByText('Em andamento')).toBeVisible(noPoll)

    // 5. Reconhecer para os lembretes e não resolve: o incidente segue aberto, a
    //    tarefa segue falhando, e o foco do botão que sumiu vai para o título.
    await expect(page.getByText('Reconhecer interrompe os lembretes; não resolve o incidente.')).toBeVisible()
    await reconhecer.focus()
    await page.keyboard.press('Enter')
    await expect(incidente.getByText('Reconhecido', { exact: true })).toBeVisible(noPoll)
    await expect(incidente.getByText(/^Reconhecido em .* · lembretes parados$/)).toBeVisible()
    await expect(reconhecer).toHaveCount(0)
    await expect(page.getByRole('heading', { name: 'Incidentes' })).toBeFocused()
    await expect(selo('Falhando')).toBeVisible()
    // Até aqui o selo pode ser o da leitura de antes do reconhecimento. O F5 lê
    // de novo do servidor, e só depois dele o selo prova que a tarefa segue
    // falhando com o incidente reconhecido.
    await page.reload()
    await expect(incidente.getByText('Reconhecido', { exact: true })).toBeVisible(noPoll)
    await expect(selo('Falhando')).toBeVisible()

    // 6. O sucesso fecha o incidente reconhecido e guarda o reconhecimento,
    //    agora sem a promessa de lembretes parados.
    await postar(url)
    await expect(selo('Em dia')).toBeVisible(noPoll)
    await expect(incidente.getByText('Resolvido')).toBeVisible(noPoll)
    await expect(incidente.getByText(/^Reconhecido em /)).toBeVisible()
    await expect(incidente.getByText(/lembretes parados/)).toHaveCount(0)
    await expect(log).toHaveCount(3, noPoll)
    await expect(log.nth(0)).toContainText('Sucesso')

    // 7. Apagar: o teto do E2E é o de 100 tarefas por conta.
    await page.getByRole('button', { name: 'Apagar' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Apagar heartbeat' }).click()
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible(noPoll)
    sobra = null

    await controlePositivo(page, 'o fim da jornada de edição do heartbeat')
  } finally {
    await encerrar()
  }
})

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
