import type { Page } from '@playwright/test'
import { imitarApi, VERSAO_DOS_TERMOS } from './api-imitada'
import { controlePositivo, expect, test } from './fixtures'

// As telas públicas do cadastro sob a CSP de produção, com a API e o api.js
// do Turnstile imitados (api-imitada.ts): o widget resolve na hora com o
// token das chaves de teste. A jornada com o binário real exigiria rede e
// e-mail; o backend fica nos testes de integração Go.

const SENHA_NOVA = 'uma senha nova bem comprida'

// Sem sessão: o refresh do boot volta 401, como para quem chega pelo e-mail.
async function semSessao(page: Page) {
  await imitarApi(page)
  await page.route('**/api/v1/auth/refresh', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
  )
}

test.beforeEach(async ({ page }) => {
  await semSessao(page)
})

test('cadastro até o confira seu e-mail', async ({ page }) => {
  await page.goto('/signup')
  await expect(page.getByRole('heading', { name: 'Criar conta no StillOn', level: 1 })).toBeVisible()
  await page.getByLabel('E-mail').fill('nova@exemplo.com')
  await page.getByLabel('Senha', { exact: true }).fill(SENHA_NOVA)
  // O api.js imitado resolveu o desafio: o botão habilita.
  await expect(page.locator('[data-turnstile-imitado]')).toBeAttached()
  const pedido = page.waitForRequest('**/api/v1/auth/signup')
  await page.getByRole('button', { name: 'Criar conta' }).click()
  expect((await pedido).postDataJSON()).toEqual({
    email: 'nova@exemplo.com',
    password: SENHA_NOVA,
    turnstile_token: 'XXXX.DUMMY.TOKEN.XXXX',
    accepted_terms_version: VERSAO_DOS_TERMOS,
  })
  await expect(page.getByRole('heading', { name: 'Confira seu e-mail', level: 1 })).toBeVisible()
  await expect(page.getByText('Abra o link em até 24 horas para concluir o cadastro.')).toBeVisible()
  await controlePositivo(page, 'cadastro')
})

test('confirmação pelo link do e-mail, com o token no fragmento e a senha do cadastro', async ({ page }) => {
  await page.goto('/confirm-email#token=token-da-caixa')
  await expect(page.getByRole('heading', { name: 'Confirme seu e-mail', level: 1 })).toBeVisible()
  // O fragmento saiu da barra, e nada foi ao servidor antes da senha.
  expect(page.url()).not.toContain('token-da-caixa')
  await page.getByLabel('Senha').fill(SENHA_NOVA)
  const pedido = page.waitForRequest('**/api/v1/auth/email/confirm')
  await page.getByRole('button', { name: 'Confirmar e-mail' }).click()
  expect((await pedido).postDataJSON()).toEqual({ token: 'token-da-caixa', password: SENHA_NOVA })
  expect((await pedido).url()).not.toContain('token-da-caixa')
  await expect(page.getByRole('heading', { name: 'E-mail confirmado', level: 1 })).toBeVisible()
  await controlePositivo(page, 'confirmação')
})

test('confirmação com senha errada mantém o formulário', async ({ page }) => {
  await page.route('**/api/v1/auth/email/confirm', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'invalid_credentials', message: '' }, trace_id: 't' } }),
  )
  await page.goto('/confirm-email#token=token-da-caixa')
  const senha = page.getByLabel('Senha')
  await senha.fill('outra senha qualquer')
  await page.getByRole('button', { name: 'Confirmar e-mail' }).click()
  await expect(page.getByText('Senha incorreta. O link continua valendo: tente de novo.')).toBeVisible()
  await expect(senha).toBeFocused()
  await controlePositivo(page, 'confirmação com senha errada')
})

test('confirmação com link inválido ou vencido', async ({ page }) => {
  await page.route('**/api/v1/auth/email/confirm', (route) =>
    route.fulfill({ status: 400, json: { error: { code: 'token_invalid', message: '' }, trace_id: 't' } }),
  )
  await page.goto('/confirm-email#token=usado')
  await page.getByLabel('Senha').fill(SENHA_NOVA)
  await page.getByRole('button', { name: 'Confirmar e-mail' }).click()
  await expect(page.getByRole('heading', { name: 'Link inválido ou vencido', level: 1 })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Entrar' })).toHaveAttribute('href', '/login')
  await controlePositivo(page, 'confirmação inválida')
})

test('esqueci a senha até o confira seu e-mail', async ({ page }) => {
  await page.goto('/forgot-password')
  await expect(page.getByRole('heading', { name: 'Redefinir a senha', level: 1 })).toBeVisible()
  await page.getByLabel('E-mail').fill('dono@exemplo.com')
  await expect(page.locator('[data-turnstile-imitado]')).toBeAttached()
  await page.getByRole('button', { name: 'Enviar link' }).click()
  await expect(page.getByRole('heading', { name: 'Confira seu e-mail', level: 1 })).toBeVisible()
  await expect(page.getByText('Abra o link em até 1 hora para escolher uma senha nova.')).toBeVisible()
  await controlePositivo(page, 'esqueci a senha')
})

test('redefinição até o login', async ({ page }) => {
  await page.goto('/reset-password#token=token-de-reset')
  await expect(page.getByRole('heading', { name: 'Escolha uma senha nova', level: 1 })).toBeVisible()
  expect(page.url()).not.toContain('token-de-reset')
  await page.getByLabel('Senha nova').fill(SENHA_NOVA)
  const pedido = page.waitForRequest('**/api/v1/auth/password/reset')
  await page.getByRole('button', { name: 'Salvar senha' }).click()
  expect((await pedido).postDataJSON()).toEqual({ token: 'token-de-reset', new_password: SENHA_NOVA })
  await expect(page.getByRole('heading', { name: 'Entrar no StillOn', level: 1 })).toBeVisible()
  await expect(page.getByText('Senha redefinida. Entre com a senha nova.')).toBeVisible()
  await controlePositivo(page, 'redefinição')
})
