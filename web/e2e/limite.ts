import type { Locator, Page } from '@playwright/test'
import { expect } from './fixtures'

// O login, a troca de senha e as rotas do 2FA dividem o balde do login (5 de
// burst por IP, 1 ficha a cada 6 s), e o E2E sai todo de um IP só. Um 429
// espera o Retry-After e envia de novo, com teto; o segundo a mais é a folga
// para a ficha já ter voltado quando o reenvio chegar.
export const ENVIOS_NO_LIMITE = 6
const RETRY_AFTER_PADRAO_S = 6
const espera = (ms: number) => new Promise((ok) => setTimeout(ok, ms))
export const esperarORetryAfter = (r: { headers(): Record<string, string> }) =>
  espera((Number(r.headers()['retry-after'] ?? RETRY_AFTER_PADRAO_S) + 1) * 1000)

// Qualquer status que não seja 429 segue, e o resultado na tela cai na asserção de quem chamou. Vale a resposta,
// e não o texto: o aviso do 429 anterior continua na tela até a resposta
// seguinte.
export async function enviarNoLimite(page: Page, rota: string, enviar: () => Promise<void>, esperado: Locator) {
  for (let i = 0; i < ENVIOS_NO_LIMITE; i++) {
    const resposta = page.waitForResponse((r) => new URL(r.url()).pathname === rota && r.request().method() === 'POST')
    await enviar()
    const r = await resposta
    if (r.status() !== 429) {
      await expect(esperado).toBeVisible()
      return
    }
    await esperarORetryAfter(r)
  }
  throw new Error(`${rota}: ainda em 429 depois de ${ENVIOS_NO_LIMITE} envios`)
}

export async function enviarLogin(page: Page, email: string, senha: string, esperado: Locator) {
  // Preenche a cada envio: a tela limpa a senha depois de uma recusa, e o
  // reenvio com o campo vazio pararia na validação, sem sair POST nenhum.
  const enviar = async () => {
    await page.getByLabel('E-mail').fill(email)
    await page.getByLabel('Senha').fill(senha)
    await page.getByRole('button', { name: 'Entrar' }).click()
  }
  await enviarNoLimite(page, '/api/v1/auth/login', enviar, esperado)
}
