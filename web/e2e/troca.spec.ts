import type { Page } from '@playwright/test'
import { controlePositivo, expect, test } from './fixtures'
import { enviarLogin, enviarNoLimite } from './limite'
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

// Os baldes do login (5 de burst por IP, que as rotas do 2FA também gastam)
// e do /totp/verify, que as specs anteriores gastaram; a mesma pausa do
// fluxo.spec.ts.
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

// As rotas do 2FA dividem o balde do login (limiteLogin, por IP) e, no E2E,
// que sai de um IP só, podem pegar 429: elas enviam por enviarNoLimite, que
// espera o Retry-After e reenvia. Nas rotas que levam código, o código sai de
// codigoNovo DENTRO do envio, a cada tentativa: um código gerado antes dos
// recuos poderia vencer (vale pouco mais de 30 s e o teto dos recuos é de 45
// s), e o ultimoPasso do totp.ts garante um passo novo por chamada, então o
// reenvio nunca repete um código (replay). O 429 não gasta o código no
// servidor (o limite roda antes do handler); o passo gasto sem uso só faz o
// codigoNovo esperar a virada, e essa espera fica antes do clique, fora do
// prazo da resposta. Os campos são preenchidos a cada tentativa, como no login:
// a tela pode limpá-los depois de uma recusa.
const ROTA_2FA = (fim: string) => `/api/v1/auth/totp/${fim}`

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
  // Cinco códigos de passos novos (mais um por reenvio depois de um 429): com a folga do servidor (totp.ts), até três
  // viradas de 30 s de espera, e o prazo de antes cobre as cinco.
  test.setTimeout(240_000)
  const email = credenciais.email
  // O status de cada resposta da autenticação vai para o log do run (sem
  // corpo nem cabeçalhos): um 429 no balde do login fica visível no CI.
  page.on('response', (r) => {
    const { pathname } = new URL(r.url())
    if (pathname.startsWith('/api/v1/auth/')) console.log(`troca: ${r.request().method()} ${pathname} ${r.status()}`)
  })

  await page.goto('/login')
  await enviarLogin(page, email, credenciais.senha, page.getByRole('heading', { name: 'Painel' }))
  await page.getByRole('link', { name: 'Conta', exact: true }).click()

  // 1. Ativar: senha, o QR (a chave em texto), o código, os códigos.
  const dialogo = page.getByRole('dialog')
  await page.getByRole('button', { name: 'Ativar' }).click()
  await enviarNoLimite(
    page,
    ROTA_2FA('enroll'),
    async () => {
      await dialogo.getByLabel('Sua senha').fill(credenciais.senha)
      await dialogo.getByRole('button', { name: 'Continuar' }).click()
    },
    dialogo.locator('code'),
  )
  const velha = await chaveNaTela(page)
  await enviarNoLimite(
    page,
    ROTA_2FA('confirm'),
    async () => {
      await dialogo.getByLabel('Código de 6 dígitos').fill(await codigoNovo(velha))
      await dialogo.getByRole('button', { name: 'Ativar' }).click()
    },
    dialogo.getByLabel('Guardei os códigos'),
  )
  await guardarCodigos(page)
  await expect(page.getByText('Ativada.', { exact: true })).toBeVisible()

  // 2. Trocar: as provas (senha e o código do app atual). O rotate devolve o
  //    rotation, 32 hex minúsculos.
  await page.getByRole('button', { name: 'Trocar autenticador' }).click()
  const respostaDoRotate = await enviarNoLimite(
    page,
    ROTA_2FA('rotate'),
    async () => {
      await dialogo.getByLabel('Sua senha').fill(credenciais.senha)
      await dialogo.getByLabel('Código do autenticador').fill(await codigoNovo(velha))
      await dialogo.getByRole('button', { name: 'Continuar' }).click()
    },
    dialogo.getByLabel('Código de 6 dígitos'),
  )
  expect(respostaDoRotate.status(), 'rotate').toBe(200)
  const { rotation } = (await respostaDoRotate.json()) as { rotation: string }
  expect(rotation).toMatch(/^[0-9a-f]{32}$/)

  // 3. O QR do app NOVO e o código dele. O painel manda de volta o rotation
  //    que recebeu, e o servidor aceita.
  const nova = await chaveNaTela(page)
  expect(nova, 'a chave do app novo').not.toBe(velha)
  const respostaDoConfirm = await enviarNoLimite(
    page,
    ROTA_2FA('rotate/confirm'),
    async () => {
      await dialogo.getByLabel('Código de 6 dígitos').fill(await codigoNovo(nova))
      await dialogo.getByRole('button', { name: 'Confirmar a troca' }).click()
    },
    dialogo.getByLabel('Guardei os códigos'),
  )
  expect(respostaDoConfirm.request().postDataJSON(), 'o corpo do rotate/confirm').toMatchObject({ rotation })
  expect(respostaDoConfirm.status(), 'rotate/confirm').toBe(200)
  await guardarCodigos(page)
  await controlePositivo(page, 'a troca de autenticador')

  // 4. Sair e entrar com o app novo: o segredo trocou de verdade.
  await page.getByRole('button', { name: 'Sair' }).click()
  await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
  // Este login é o sétimo pedido seguido no balde do login (o entrar do passo
  // 1 e as cinco rotas do 2FA): o enviarLogin recua no 429.
  const campoDoCodigo = page.getByLabel('Código do aplicativo autenticador')
  await enviarLogin(page, email, credenciais.senha, campoDoCodigo)
  await campoDoCodigo.fill(await codigoNovo(nova))
  await page.getByRole('button', { name: 'Verificar' }).click()
  // O ?next= volta a Configurações, como no fluxo.spec.ts.
  await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()

  // 5. Desativar com o app novo: o usuário volta como estava.
  await page.getByRole('button', { name: 'Desativar' }).click()
  await enviarNoLimite(
    page,
    ROTA_2FA('disable'),
    async () => {
      await dialogo.getByLabel('Sua senha').fill(credenciais.senha)
      await dialogo.getByLabel('Código do autenticador').fill(await codigoNovo(nova))
      await dialogo.getByRole('button', { name: 'Desativar' }).click()
    },
    page.getByText('Desativada.', { exact: true }),
  )
  await controlePositivo(page, 'o login com o app novo e o desativar')
})
