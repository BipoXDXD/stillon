import type { CDPSession, Page } from '@playwright/test'
import { controlePositivo, expect, registrarCsp, test, violacoes } from './fixtures'
import { contarAuditoria } from './pilha'

// Duas abas do MESMO contexto (dividem cookies, não memória) recarregando
// juntas: o boot de cada uma é um refresh. Para a corrida não depender de
// sorte, o teste segura a RESPOSTA de cada /auth/refresh no navegador
// (CDP, Fetch no estágio de resposta: a requisição já saiu com o cookie
// daquele instante, e o Set-Cookie ainda não foi aplicado) até as duas
// abas terem a sua em mãos — ou até 1,5 s. Sem a Web Lock de sessao.ts, as
// duas saem com o mesmo cookie, a segunda é reúso, e o servidor revoga a
// família: uma aba cai no login. Com a trava, a segunda aba nem envia antes
// de a primeira terminar; a espera vence pelo prazo, e as duas continuam
// dentro. Latência via emulação de rede não bastou: com o servidor rápido,
// a corrida só aparecia às vezes (achado na validação do Plano 6).
async function segurarRefreshes(sessoes: CDPSession[]) {
  let pausadas: { cdp: CDPSession; requestId: string }[] = []
  let prazo: ReturnType<typeof setTimeout> | null = null
  // liberar desarma o prazo: um prazo velho de uma rodada, disparando no meio
  // da seguinte, soltaria a primeira resposta antes de a outra aba enviar a
  // sua, e a sabotagem passaria às vezes (achado na validação do Plano 6).
  const liberar = () => {
    if (prazo) clearTimeout(prazo)
    prazo = null
    const lote = pausadas
    pausadas = []
    // A aba pode ter navegado (ou fechado) com a resposta pausada: o CDP
    // recusa o continue, e isso não é falha do teste.
    for (const p of lote) p.cdp.send('Fetch.continueResponse', { requestId: p.requestId }).catch(() => {})
  }
  for (const cdp of sessoes) {
    await cdp.send('Fetch.enable', { patterns: [{ urlPattern: '*/api/v1/auth/refresh', requestStage: 'Response' }] })
    cdp.on('Fetch.requestPaused', (e) => {
      pausadas.push({ cdp, requestId: e.requestId })
      if (pausadas.length >= sessoes.length) {
        liberar()
      } else if (!prazo) {
        prazo = setTimeout(liberar, 1_500)
      }
    })
  }
}

async function entrar(page: Page, email: string, senha: string) {
  await page.goto('/login')
  await page.getByLabel('E-mail').fill(email)
  await page.getByLabel('Senha').fill(senha)
  await page.getByRole('button', { name: 'Entrar' }).click()
  await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
}

test('duas abas recarregando juntas continuam dentro', async ({ page, context, credenciais }) => {
  await entrar(page, credenciais.email, credenciais.senha)
  const outra = await context.newPage()
  // A segunda aba também coleta as violações de CSP (a do fixture já coleta).
  await registrarCsp(outra)
  await outra.goto('/')
  await expect(outra.getByRole('heading', { name: 'Painel' })).toBeVisible()
  // Controle positivo nas duas abas (spec do Plano 6 §7.4): sem ele, "zero
  // violações" seria verde também com o coletor quebrado.
  await controlePositivo(page)
  await controlePositivo(outra)

  await segurarRefreshes([await context.newCDPSession(page), await context.newCDPSession(outra)])
  const assentou = /^(Painel|Entrar no Vigilume)$/
  for (let rodada = 0; rodada < 2; rodada++) {
    await Promise.all([page.reload(), outra.reload()])
    // As duas abas terminam o boot (dentro ou no login) antes de olhar o
    // banco: o servidor não viu reúso nenhum (spec do Plano 6 §7.4). Só com a
    // pilha própria; contra uma pilha externa o teste não alcança o banco.
    await expect(page.getByRole('heading', { name: assentou })).toBeVisible()
    await expect(outra.getByRole('heading', { name: assentou })).toBeVisible()
    const reusos = contarAuditoria('session.reuse_detected')
    if (reusos !== null) expect(reusos, 'auditorias session.reuse_detected').toBe(0)
    await expect(page.getByRole('heading', { name: 'Painel' })).toBeVisible()
    await expect(outra.getByRole('heading', { name: 'Painel' })).toBeVisible()
  }
  await expect.poll(() => violacoes(page), 'CSP, primeira aba').toEqual([])
  await expect.poll(() => violacoes(outra), 'CSP, segunda aba').toEqual([])
})
