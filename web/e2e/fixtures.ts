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
  await page.exposeFunction('stillonViolacao', (v: string) => {
    lista.push(v)
  })
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => {
      const w = window as unknown as { stillonViolacao: (v: string) => void }
      // A violação do controle positivo chega com o <style> dele como alvo
      // (o evento é disparado no elemento e sobe até o document).
      const marca = e.target instanceof Element ? e.target.getAttribute('data-controle') : null
      w.stillonViolacao(marca ? `controle ${marca}` : `${e.violatedDirective} ${e.blockedURI} ${e.sample}`)
    })
  })
}

let controles = 0

// Controle positivo e barreira da CSP (E7 do Plano 10). Um <style> sem nonce
// TEM de virar violação: sem isto, "zero violações" seria verde também com o
// coletor quebrado. O <style> leva uma marca própria, e a barreira espera a
// violação DELE: as violações provocadas antes dela já chegaram (o aviso
// vem na ordem em que a página as dispara). Então tira a marca e exige que
// o resto seja []. Antes, um expect.poll(...).toEqual([]) passava na
// primeira leitura vazia, antes de uma violação tardia chegar, e o
// controle tirava do fim da lista a violação que chegasse primeiro, que
// podia não ser a dele.
export async function controlePositivo(page: Page, contexto: string) {
  const marca = String(++controles)
  await page.evaluate((m) => {
    const s = document.createElement('style')
    s.dataset.controle = m
    s.textContent = '.controle-positivo{color:red}'
    document.head.append(s)
  }, marca)
  await expect
    .poll(() => violacoes(page).includes(`controle ${marca}`), `${contexto}: a violação do controle positivo`)
    .toBe(true)
  await page.evaluate((m) => document.querySelector(`style[data-controle="${m}"]`)?.remove(), marca)
  const lista = violacoes(page)
  lista.splice(lista.indexOf(`controle ${marca}`), 1)
  expect([...lista], `${contexto}: violações de CSP`).toEqual([])
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
