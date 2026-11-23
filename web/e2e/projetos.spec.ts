import { randomUUID } from 'node:crypto'
import type { Page } from '@playwright/test'
import { cadenciarRecursos } from './cadencia'
import { controlePositivo, expect, test } from './fixtures'
import { ENVIOS_NO_LIMITE, enviarLogin, esperarORetryAfter } from './limite'

// Projetos no painel, contra o binário real: criar um projeto pelo seletor,
// cadastrar um alvo nele, trocar de projeto e ver que o alvo de um não
// aparece no outro; um projeto que não existe é a página não encontrada no
// painel e 404 na API. O projeto criado fica: não há DELETE de projeto até o
// PR 6. O alvo sai no afterEach, verde ou vermelho.

test.use({ actionTimeout: 15_000 })

const MUTACAO = { 'X-Requested-With': 'stillon' }
// A mesma pausa do fluxo.spec.ts: os baldes do login (5 de burst por IP) e o
// por usuário, que as specs anteriores gastaram, enchem de novo.
const RECARGA_DOS_BALDES_MS = 21_000
const UUID = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'

test.beforeAll(async () => {
  test.setTimeout(RECARGA_DOS_BALDES_MS + 10_000)
  await new Promise((ok) => setTimeout(ok, RECARGA_DOS_BALDES_MS))
})

// O alvo cadastrado no projeto B, para o afterEach apagar.
let sobra: { projeto: string; alvo: string } | null = null
let cabecalhos: Record<string, string> = {}

test.afterEach(async ({ request }) => {
  if (sobra) {
    const { projeto, alvo } = sobra
    sobra = null
    const r = await request.delete(`/api/v1/projects/${projeto}/targets/${alvo}`, { headers: cabecalhos })
    expect([204, 404], `apagar o alvo ${alvo}`).toContain(r.status())
  }
  if (cabecalhos.Authorization) await request.post('/api/v1/auth/logout', { headers: MUTACAO })
  cabecalhos = {}
})

const seletor = (page: Page) => page.getByRole('button', { name: /^Projeto:/ })

// O painel com os dados do projeto já na tela: sem o esqueleto e sem o
// "Carregando…". Sem isto, "o alvo não aparece" passaria com a lista ainda
// vindo.
async function painelCarregado(page: Page) {
  await expect(page.getByRole('heading', { name: 'Painel', level: 1 })).toBeVisible()
  await expect(page.locator('#conteudo [aria-busy="true"]')).toHaveCount(0)
  await expect(page.getByText('Carregando…', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('table', { name: /^Monitores/ }).or(page.getByRole('heading', { name: 'Nenhum serviço vigiado ainda.' }))).toBeVisible()
}

async function trocarPara(page: Page, nome: string) {
  await seletor(page).click()
  await page.getByRole('menu').getByRole('menuitemradio', { name: nome, exact: true }).click()
  await expect(seletor(page)).toHaveAccessibleName(`Projeto: ${nome}`)
}

test('criar, trocar e isolar: o alvo de um projeto não aparece no outro', async ({ page, request, credenciais }, info) => {
  test.setTimeout(120_000)
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const marca = Date.now()
    const nomeDoProjeto = `E2E B ${marca}`
    const nomeDoAlvo = `E2E alvo de B ${marca}`

    // O token da API, para a limpeza e para o 404 do fim.
    for (let envio = 1; ; envio++) {
      const r = await request.post('/api/v1/auth/login', {
        headers: MUTACAO,
        data: { email: credenciais.email, password: credenciais.senha },
      })
      if (r.status() !== 429 || envio === ENVIOS_NO_LIMITE) {
        expect(r.status(), 'login pela API').toBe(200)
        const { access_token: token } = (await r.json()) as { access_token: string }
        cabecalhos = { Authorization: `Bearer ${token}`, ...MUTACAO }
        break
      }
      await esperarORetryAfter(r)
    }
    const lista = await request.get('/api/v1/projects', { headers: cabecalhos })
    expect(lista.status(), 'a lista de projetos').toBe(200)
    const { items } = (await lista.json()) as { items: { id: string; name: string; is_default: boolean; role: string }[] }
    const padrao = items.find((p) => p.is_default && p.role === 'owner')
    expect(padrao, 'o Projeto padrão').toBeDefined()

    // 1. Login: a raiz leva ao projeto padrão.
    await page.goto('/login')
    await enviarLogin(page, credenciais.email, credenciais.senha, page.getByRole('heading', { name: 'Painel' }))
    await expect(page).toHaveURL(new RegExp(`/projects/${padrao?.id}/$`))
    await expect(seletor(page)).toHaveAccessibleName(`Projeto: ${padrao?.name}`)

    // 2. Criar B pelo seletor: o painel abre nele.
    await seletor(page).click()
    await page.getByRole('menu').getByRole('menuitem', { name: 'Novo projeto' }).click()
    const dialogo = page.getByRole('dialog', { name: 'Novo projeto' })
    await dialogo.getByLabel('Nome do projeto').fill(nomeDoProjeto)
    await dialogo.getByRole('button', { name: 'Criar projeto' }).click()
    await expect(dialogo).toBeHidden()
    await expect(seletor(page)).toHaveAccessibleName(`Projeto: ${nomeDoProjeto}`)
    await expect(page).toHaveURL(new RegExp(`/projects/(${UUID})/$`))
    const projetoB = new URL(page.url()).pathname.split('/')[2] ?? ''
    expect(projetoB).not.toBe(padrao?.id)
    await painelCarregado(page)
    await expect(page).toHaveTitle(`Painel — ${nomeDoProjeto} — StillOn`)

    // 3. Um alvo em B. O .invalid (RFC 2606) nunca resolve: sem rede externa.
    await page.getByRole('link', { name: 'Cadastrar alvo' }).click()
    await expect(page.getByRole('heading', { name: 'Novo alvo' })).toBeVisible()
    await page.getByLabel('Nome', { exact: true }).fill(nomeDoAlvo)
    await page.getByLabel('URL').fill('https://stillon-e2e.invalid/')
    await page.getByRole('button', { name: 'Cadastrar' }).click()
    await expect(page.getByRole('heading', { name: nomeDoAlvo })).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/projects/${projetoB}/targets/(${UUID})$`))
    sobra = { projeto: projetoB, alvo: new URL(page.url()).pathname.split('/')[4] ?? '' }

    // 4. Do detalhe do alvo, a troca vai ao painel do padrão (o monitor de
    //    um projeto não existe no outro), e o alvo de B não aparece.
    //    O cache do TanStack (staleTime de 10 s) ainda guarda o painel do
    //    passo 1, e um "não aparece" passaria antes de qualquer pedido. Por
    //    isso a página recarrega, e o teste espera a lista vinda da rota do
    //    padrão: um painel que pedisse a rota antiga (ou a de B) não chega aqui.
    await trocarPara(page, padrao?.name ?? '')
    await expect(page).toHaveURL(new RegExp(`/projects/${padrao?.id}/$`))
    const listaDoPadrao = page.waitForResponse(
      (resp) =>
        resp.request().method() === 'GET' &&
        new URL(resp.url()).pathname.startsWith(`/api/v1/projects/${padrao?.id}/targets`) &&
        resp.ok(),
    )
    await page.reload()
    expect(await (await listaDoPadrao).text(), 'a lista do padrão vinda da API').not.toContain(nomeDoAlvo)
    await painelCarregado(page)
    await expect(page.getByRole('link', { name: nomeDoAlvo, exact: true })).toHaveCount(0)

    // 5. De volta a B, ele aparece.
    await trocarPara(page, nomeDoProjeto)
    await expect(page).toHaveURL(new RegExp(`/projects/${projetoB}/$`))
    await painelCarregado(page)
    await expect(page.getByRole('link', { name: nomeDoAlvo, exact: true })).toBeVisible()
    await controlePositivo(page, 'troca de projeto')

    // 6. Um projeto que não existe: a página não encontrada, e a API diz 404.
    const inexistente = randomUUID()
    await page.goto(`/projects/${inexistente}/`)
    await expect(page.getByRole('heading', { name: 'Página não encontrada', level: 1 })).toBeVisible()
    const r = await request.get(`/api/v1/projects/${inexistente}/targets`, { headers: cabecalhos })
    expect(r.status(), 'alvos de um projeto que não existe').toBe(404)
  } finally {
    await encerrar()
  }
})
