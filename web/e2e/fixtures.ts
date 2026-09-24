import { test as base, expect, type Page } from '@playwright/test'

// O listener de CSP é do TESTE (addInitScript), não do painel: o app de
// produção não carrega nada disso. As violações vão para o processo do teste
// (exposeFunction), e por isso sobrevivem a F5 e navegação: um array na
// própria página zeraria a cada carga e esconderia a violação da tela
// anterior.
const registros = new WeakMap<Page, string[]>()

export function violacoes(page: Page): string[] {
  return registros.get(page) ?? []
}

// Toda página que o teste abre precisa do coletor: a do fixture o recebe
// sozinha; uma aba aberta pelo teste (context.newPage) chama esta função.
export async function registrarCsp(page: Page) {
  const lista: string[] = []
  registros.set(page, lista)
  await page.exposeFunction('vigilumeViolacao', (v: string) => {
    lista.push(v)
  })
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      const w = window as unknown as { vigilumeViolacao: (v: string) => void }
      w.vigilumeViolacao(`${e.violatedDirective} ${e.blockedURI} ${e.sample}`)
    })
  })
}

// Controle positivo: um <style> sem nonce TEM de virar violação. Sem isto,
// "zero violações" seria verde também com o listener quebrado. Devolve a
// lista sem a violação provocada.
export async function controlePositivo(page: Page) {
  const antes = violacoes(page).length
  await page.evaluate(() => {
    const s = document.createElement('style')
    s.textContent = '.controle-positivo{color:red}'
    document.head.append(s)
  })
  await expect.poll(() => violacoes(page).length).toBe(antes + 1)
  violacoes(page).pop()
}

export const test = base.extend<{ credenciais: { email: string; senha: string } }>({
  // O Playwright exige desestruturação no primeiro parâmetro de um fixture.
  // oxlint-disable-next-line no-empty-pattern
  baseURL: async ({}, use) => {
    await use(process.env.E2E_BASE_URL)
  },
  // oxlint-disable-next-line no-empty-pattern
  credenciais: async ({}, use) => {
    await use({ email: process.env.E2E_EMAIL ?? '', senha: process.env.E2E_PASSWORD ?? '' })
  },
  page: async ({ page }, use) => {
    await registrarCsp(page)
    await use(page)
  },
})

export { expect }
