import AxeBuilder from '@axe-core/playwright'
import type { Locator, Page } from '@playwright/test'
import { me } from '../src/test/fixtures'
import { agora, imitarApi, NO_PROJETO } from './api-imitada'
import { controlePositivo, expect, test } from './fixtures'

// A conta logada que ainda não confirmou o e-mail, com a API imitada
// (api-imitada.ts): o /me volta com email_verified false. O painel abre, o
// aviso fica no topo do conteúdo, e criar recurso dá 403 email_not_verified.

const EMAIL_DA_CONTA = me({ created_at: agora }).email
const TEXTO_DO_AVISO = 'Confirme seu e-mail para criar monitores, canais e páginas.'

async function contaNaoConfirmada(page: Page) {
  await imitarApi(page)
  // As rotas registradas depois valem antes das do imitarApi.
  await page.route('**/api/v1/me', (route) => route.fulfill({ json: me({ created_at: agora, email_verified: false }) }))
}

const aviso = (raiz: Locator | Page) => raiz.getByRole('complementary', { name: 'Confirmação de e-mail' })

test.beforeEach(async ({ page }) => {
  await contaNaoConfirmada(page)
})

test('o aviso aparece no topo do conteúdo, com o e-mail da conta', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Painel', level: 1 })).toBeVisible()
  await expect(aviso(page)).toBeVisible()
  await expect(aviso(page)).toContainText(TEXTO_DO_AVISO)
  await expect(aviso(page)).toContainText(`Enviamos um link para ${EMAIL_DA_CONTA}.`)
  await expect(aviso(page.locator('#conteudo'))).toBeVisible()
  await controlePositivo(page, 'aviso')
})

test('reenviar mostra que o pedido foi aceito', async ({ page }) => {
  await page.goto('/')
  const pedido = page.waitForRequest(
    (r) => r.url().endsWith('/api/v1/auth/email/resend') && r.method() === 'POST',
  )
  await aviso(page).getByRole('button', { name: 'Reenviar e-mail' }).click()
  expect((await pedido).headers()['x-requested-with']).toBe('stillon')
  await expect(aviso(page).getByRole('status')).toHaveText('Pedido de reenvio recebido. Se o e-mail ainda precisar de confirmação, confira sua caixa de entrada.')
  await expect(aviso(page).getByRole('button', { name: 'Reenviar e-mail' })).toHaveCount(0)
  // O foco não cai no <body> quando o botão dá lugar ao texto.
  await expect(page.locator('body')).not.toBeFocused()
  await controlePositivo(page, 'reenvio')
})

test('criar um alvo mostra a mensagem de e-mail não confirmado', async ({ page }) => {
  await page.route(`**/api/v1${NO_PROJETO}/targets`, (route) =>
    route.request().method() === 'POST'
      ? route.fulfill({ status: 403, json: { error: { code: 'email_not_verified', message: '' }, trace_id: 't' } })
      : route.fallback(),
  )
  await page.goto(NO_PROJETO + '/targets/new')
  await expect(page.getByRole('heading', { name: 'Novo alvo', level: 1 })).toBeVisible()
  await page.getByLabel('Nome', { exact: true }).fill('API nova')
  await page.getByLabel('URL', { exact: true }).fill('https://api.exemplo.com/health')
  await page.getByRole('button', { name: 'Cadastrar', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: 'Confirme seu e-mail para criar recursos.' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Novo alvo', level: 1 })).toBeVisible()
  await controlePositivo(page, 'criar sem confirmar')
})

for (const tema of ['claro', 'escuro'] as const) {
  test(`acessibilidade do aviso, antes e depois do reenvio, tema ${tema}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
    await page.goto('/')
    await expect(aviso(page)).toBeVisible()
    await expect(page.locator('html')).toHaveClass(tema === 'escuro' ? /dark/ : /^(?!.*dark)/)
    const antes = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(antes.violations.map((v) => `${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
    await aviso(page).getByRole('button', { name: 'Reenviar e-mail' }).click()
    await expect(aviso(page)).toContainText('Pedido de reenvio recebido.')
    const depois = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(depois.violations.map((v) => `${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
    await controlePositivo(page, `aviso, tema ${tema}`)
  })
}
