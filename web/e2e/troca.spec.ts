import type { Page } from '@playwright/test'
import { controlePositivo, expect, test } from './fixtures'
import { desligar2FA, podeDesligar2FA } from './pilha'
import { codigoNovo } from './totp'

// A troca de autenticador contra o binário (A7 do Plano 10 com a G1 do
// painel). O rotate devolve o rotation e o rotate/confirm o exige: os dois
// lados só foram validados separados (o painel contra a API falsa, o
// servidor pelos testes de integração em Go), e a Review Focus do plano
// pediu uma troca ponta a ponta. Aqui o painel de produção, embutido no
// binário, ativa o 2FA, troca de aplicativo e entra de novo com o novo.
//
// O StillOn tem um usuário só, o das outras specs: este teste liga o 2FA
// nele e, no fim, desliga. Uma falha no meio o deixaria ligado para as specs
// seguintes (uma spec nova depois de "troca", --repeat-each) ou para a
// próxima rodada contra a mesma pilha. Por isso o afterAll desliga o 2FA pelo
// CLI (stillon user reset-2fa) mesmo com o teste vermelho, e o beforeAll faz
// o mesmo antes, para uma rodada anterior interrompida não deixar o teste
// começar com o 2FA ligado (M2 da revisão do painel do Plano 10). Roda na
// pilha própria e contra o compose (o job e2e do CI, com E2E_COMPOSE_SERVICE);
// contra outra pilha externa, onde não há como rodar o CLI, é pulado.

// Os baldes do login (5 de burst por IP) e do /totp/verify, que o fluxo
// acabou de gastar; a mesma pausa do fluxo.spec.ts.
const RECARGA_DOS_BALDES_MS = 21_000

test.beforeAll(async () => {
  test.skip(!podeDesligar2FA(), 'sem como desligar o 2FA pelo CLI: o teste liga o 2FA do único usuário')
  test.setTimeout(RECARGA_DOS_BALDES_MS + 30_000)
  desligar2FA(process.env.E2E_EMAIL!)
  await new Promise((ok) => setTimeout(ok, RECARGA_DOS_BALDES_MS))
})

test.afterAll(() => {
  if (podeDesligar2FA()) desligar2FA(process.env.E2E_EMAIL!)
})

async function chaveNaTela(page: Page): Promise<string> {
  const chave = (await page.getByRole('dialog').locator('code').innerText()).trim()
  expect(chave, 'a chave do QR code').toMatch(/^[A-Z2-7]+=*$/)
  return chave
}

async function guardarCodigos(page: Page) {
  const dialogo = page.getByRole('dialog')
  await expect(dialogo.getByRole('listitem')).toHaveCount(10)
  await dialogo.getByLabel('Guardei os códigos').check()
  await dialogo.getByRole('button', { name: 'Concluir' }).click()
  await expect(dialogo).toHaveCount(0)
}

test('ativar o 2FA, trocar de autenticador com o rotation, entrar com o app novo e desativar', async ({
  page,
  credenciais,
}) => {
  // Cinco códigos novos: até cinco timesteps de 30 s de espera.
  test.setTimeout(240_000)
  const email = credenciais.email

  await page.goto('/login')
  await page.getByLabel('E-mail').fill(email)
  await page.getByLabel('Senha').fill(credenciais.senha)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
  await page.getByRole('link', { name: 'Configurações' }).click()

  // 1. Ativar: senha, o QR (a chave em texto), o código, os códigos.
  await page.getByRole('button', { name: 'Ativar' }).click()
  await page.getByRole('dialog').getByLabel('Sua senha').fill(credenciais.senha)
  await page.getByRole('dialog').getByRole('button', { name: 'Continuar' }).click()
  const velha = await chaveNaTela(page)
  await page.getByRole('dialog').getByLabel('Código de 6 dígitos').fill(await codigoNovo(velha))
  await page.getByRole('dialog').getByRole('button', { name: 'Ativar' }).click()
  await guardarCodigos(page)
  await expect(page.getByText('Ativada.', { exact: true })).toBeVisible()

  // 2. Trocar: as provas (senha e o código do app atual). O rotate devolve o
  //    rotation, 32 hex minúsculos.
  await page.getByRole('button', { name: 'Trocar autenticador' }).click()
  await page.getByRole('dialog').getByLabel('Sua senha').fill(credenciais.senha)
  await page.getByRole('dialog').getByLabel('Código do autenticador').fill(await codigoNovo(velha))
  const rotate = page.waitForResponse((r) => r.url().endsWith('/api/v1/auth/totp/rotate'))
  await page.getByRole('dialog').getByRole('button', { name: 'Continuar' }).click()
  const respostaDoRotate = await rotate
  expect(respostaDoRotate.status(), 'rotate').toBe(200)
  const { rotation } = (await respostaDoRotate.json()) as { rotation: string }
  expect(rotation).toMatch(/^[0-9a-f]{32}$/)

  // 3. O QR do app NOVO e o código dele. O painel manda de volta o rotation
  //    que recebeu, e o servidor aceita.
  const nova = await chaveNaTela(page)
  expect(nova, 'a chave do app novo').not.toBe(velha)
  await page.getByRole('dialog').getByLabel('Código de 6 dígitos').fill(await codigoNovo(nova))
  const confirm = page.waitForRequest((r) => r.url().endsWith('/api/v1/auth/totp/rotate/confirm'))
  await page.getByRole('dialog').getByRole('button', { name: 'Confirmar a troca' }).click()
  const pedido = await confirm
  expect(pedido.postDataJSON(), 'o corpo do rotate/confirm').toMatchObject({ rotation })
  expect((await pedido.response())?.status(), 'rotate/confirm').toBe(200)
  await guardarCodigos(page)
  await controlePositivo(page, 'a troca de autenticador')

  // 4. Sair e entrar com o app novo: o segredo trocou de verdade.
  await page.getByRole('button', { name: 'Sair' }).click()
  await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
  await page.getByLabel('E-mail').fill(email)
  await page.getByLabel('Senha').fill(credenciais.senha)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await page.getByLabel('Código do aplicativo autenticador').fill(await codigoNovo(nova))
  await page.getByRole('button', { name: 'Verificar' }).click()
  // O ?next= volta a Configurações, como no fluxo.spec.ts.
  await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()

  // 5. Desativar com o app novo: o usuário volta como estava.
  await page.getByRole('button', { name: 'Desativar' }).click()
  await page.getByRole('dialog').getByLabel('Sua senha').fill(credenciais.senha)
  await page.getByRole('dialog').getByLabel('Código do autenticador').fill(await codigoNovo(nova))
  await page.getByRole('dialog').getByRole('button', { name: 'Desativar' }).click()
  await expect(page.getByText('Desativada.', { exact: true })).toBeVisible()
  await controlePositivo(page, 'o login com o app novo e o desativar')
})
