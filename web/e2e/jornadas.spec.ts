import type { APIRequestContext, APIResponse, Locator, Page } from '@playwright/test'
import { cadenciarRecursos } from './cadencia'
import { controlePositivo, expect, test } from './fixtures'
import { podeRedefinirSenha, redefinirSenha } from './pilha'

// As jornadas críticas que o fluxo.spec.ts não percorre, contra a pilha real:
// publicar a status page pelo painel, editar um alvo e reconhecer o incidente
// dele na aba Incidentes, configurar um canal de aviso (o que a pilha local
// consegue provar: as recusas, sem nada sair para fora) e trocar a senha.
// Os dados de cada teste nascem nele, com nome próprio, e a limpeza é uma
// pilha de desfazer que roda no afterEach, verde ou vermelho.

test.use({ actionTimeout: 15_000 })

const MUTACAO = { 'X-Requested-With': 'stillon' }
// A mesma pausa do fluxo.spec.ts: os baldes do login (5 de burst por IP) e o
// por usuário, que as specs anteriores gastaram, enchem de novo.
const RECARGA_DOS_BALDES_MS = 21_000
// Um 429 do login ou da troca de senha (o balde do IP é dividido com o painel)
// espera o Retry-After e envia de novo, com teto; o segundo a mais é a folga
// para a ficha já ter voltado quando o reenvio chegar.
const ENVIOS_NO_LIMITE = 6
const RETRY_AFTER_PADRAO_S = 6
const espera = (ms: number) => new Promise((ok) => setTimeout(ok, ms))
const esperarORetryAfter = (r: { headers(): Record<string, string> }) =>
  espera((Number(r.headers()['retry-after'] ?? RETRY_AFTER_PADRAO_S) + 1) * 1000)

test.beforeAll(async () => {
  test.setTimeout(RECARGA_DOS_BALDES_MS + 10_000)
  await espera(RECARGA_DOS_BALDES_MS)
})

type Etapa = { nome: string; fazer: (api: APIRequestContext, cabecalhos: Record<string, string>) => Promise<APIResponse> }
let desfazer: Etapa[] = []

test.beforeEach(() => {
  desfazer = []
})

// Contexto novo: o do teste pode ter ficado ocioso além do IdleTimeout do
// servidor (pagina-publica.spec.ts). O último a entrar sai primeiro, e uma
// etapa que falha não impede as seguintes.
test.afterEach(async ({ playwright, baseURL, credenciais }) => {
  if (!desfazer.length) return
  const etapas = desfazer
  desfazer = []
  const api = await playwright.request.newContext({ baseURL })
  const falhas: string[] = []
  try {
      const cabecalhos = await entrarPelaApi(api, credenciais.email, credenciais.senha)
      for (const { nome, fazer } of etapas) {
        const r = await fazer(api, cabecalhos).catch((e: unknown) => String(e))
        if (typeof r === 'string') falhas.push(`${nome}: ${r}`)
        else if (!r.ok() && r.status() !== 404) falhas.push(`${nome}: HTTP ${r.status()}`)
      }
      await api.post('/api/v1/auth/logout', { headers: MUTACAO })
  } finally {
    await api.dispose()
  }
  expect(falhas, 'a limpeza').toEqual([])
})

async function entrarPelaApi(api: APIRequestContext, email: string, senha: string): Promise<Record<string, string>> {
  for (let envio = 1; ; envio++) {
    const r = await api.post('/api/v1/auth/login', { headers: MUTACAO, data: { email, password: senha } })
    if (r.status() !== 429 || envio === ENVIOS_NO_LIMITE) {
      expect(r.status(), 'login pela API').toBe(200)
      const { access_token: token } = (await r.json()) as { access_token: string }
      return { Authorization: `Bearer ${token}`, ...MUTACAO }
    }
    await esperarORetryAfter(r)
  }
}

// O login tem o balde por IP e a troca de senha o balde da conta (5 de burst e
// 1 ficha a cada 6 s cada um), e esta spec passa dos dois. Qualquer status que não seja 429
// segue, e o resultado na tela cai na asserção de quem chamou. Vale a resposta,
// e não o texto: o aviso do 429 anterior continua na tela até a resposta
// seguinte.
async function enviarNoLimite(page: Page, rota: string, enviar: () => Promise<void>, esperado: Locator) {
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

async function enviarLogin(page: Page, email: string, senha: string, esperado: Locator) {
  // Preenche a cada envio: a tela limpa a senha depois de uma recusa, e o
  // reenvio com o campo vazio pararia na validação, sem sair POST nenhum.
  const enviar = async () => {
    await page.getByLabel('E-mail').fill(email)
    await page.getByLabel('Senha').fill(senha)
    await page.getByRole('button', { name: 'Entrar' }).click()
  }
  await enviarNoLimite(page, '/api/v1/auth/login', enviar, esperado)
}

async function entrarPeloPainel(page: Page, email: string, senha: string) {
  await page.goto('/login')
  await enviarLogin(page, email, senha, page.getByRole('heading', { name: 'Painel' }))
}

// O alvo .invalid (RFC 2606) passa pelo cadastro e nunca resolve: fica "fora
// do ar" sem rede externa, como no fluxo.spec.ts.
async function criarAlvo(api: APIRequestContext, cabecalhos: Record<string, string>, nome: string): Promise<string> {
  const r = await api.post('/api/v1/targets', {
    headers: cabecalhos,
    data: { name: nome, url: 'https://stillon-e2e.invalid/', method: 'GET', interval_sec: 300, timeout_ms: 5000, expected_status: 200 },
  })
  expect(r.status(), 'criar o alvo').toBe(201)
  const { id } = (await r.json()) as { id: string }
  desfazer.unshift({ nome: `apagar o alvo ${nome}`, fazer: (a, c) => a.delete(`/api/v1/targets/${id}`, { headers: c }) })
  return id
}

const aba = (page: Page, nome: string | RegExp) => page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: nome })

type Pagina = { id: string; slug: string; title: string; published: boolean; items: { target_id: string; display_name: string }[] }

test('status page pelo painel: publicar com um alvo, abrir a página pública e despublicar', async ({ page, request, credenciais }, info) => {
  test.setTimeout(120_000)
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const marca = Date.now()
    const nomeDoAlvo = `E2E status ${marca}`
    const exibicao = `Serviço exibido ${marca}`
    const titulo = `Status E2E ${marca}`
    const cabecalhos = await entrarPelaApi(request, credenciais.email, credenciais.senha)
    await criarAlvo(request, cabecalhos, nomeDoAlvo)

    // Há uma status page por usuário: a que existir volta a ser o que era; a
    // criada aqui sai do ar, vazia (a API não apaga status page).
    const lista = await request.get('/api/v1/status-pages', { headers: cabecalhos })
    expect(lista.status(), 'listar as status pages').toBe(200)
    const original = ((await lista.json()) as { items: Pagina[] }).items[0]
    desfazer.unshift({
      nome: 'restaurar a status page',
      fazer: async (a, c) => {
        const atual = ((await (await a.get('/api/v1/status-pages', { headers: c })).json()) as { items: Pagina[] }).items[0]
        if (!atual) return a.get('/healthz')
        const corpo = original
          ? { title: original.title, slug: original.slug, published: original.published, items: original.items }
          : { published: false, items: [] }
        return a.patch(`/api/v1/status-pages/${atual.id}`, { headers: c, data: corpo })
      },
    })
    await request.post('/api/v1/auth/logout', { headers: MUTACAO })

    await entrarPeloPainel(page, credenciais.email, credenciais.senha)
    await aba(page, 'Status').click()
    await expect(page.getByRole('heading', { level: 1, name: 'Status page' })).toBeVisible()
    // Sem status page, o slug é o sugerido pelo título ao sair do campo.
    await page.getByLabel('Título').fill(titulo)
    await page.getByLabel('Título').blur()
    if (!original) await expect(page.getByLabel('Endereço (slug)')).toHaveValue(`status-e2e-${marca}`)
    const slug = await page.getByLabel('Endereço (slug)').inputValue()
    await page.getByLabel(/^Publicada/).check()
    await page.getByLabel('Acrescentar alvo').selectOption({ label: nomeDoAlvo })
    await page.getByRole('button', { name: 'Acrescentar', exact: true }).click()
    await page.getByLabel(`Nome de exibição de ${nomeDoAlvo}`).fill(exibicao)
    await page.getByRole('button', { name: /^(Salvar|Criar status page)$/ }).click()
    await expect(page.getByText(/^Status page salva\./)).toBeVisible()
    // O link público aparece com o slug salvo, e só publicado ganha o Abrir.
    await expect(page.getByText(new RegExp(`/p/${slug}$`))).toBeVisible()
    await expect(page.getByRole('link', { name: 'Abrir' })).toHaveAttribute('href', new RegExp(`/p/${slug}$`))

    // A página pública, sem sessão nenhuma envolvida: o título e o nome de
    // exibição escolhidos no painel, nunca o nome interno do alvo.
    await page.goto(`/p/${slug}`)
    await expect(page.getByRole('heading', { level: 1, name: titulo })).toBeVisible()
    await expect(page.getByText(exibicao, { exact: true })).toBeVisible()
    await expect(page.getByText(nomeDoAlvo)).toHaveCount(0)
    await controlePositivo(page, 'a página pública publicada pelo painel')

    // Despublicar: o painel avisa, e a página some. O request não tem o cache
    // do navegador (max-age=30), então o 404 é o do servidor agora.
    await page.goto('/status-pages')
    await expect(page.getByLabel(/^Publicada/)).toBeChecked()
    await page.getByLabel(/^Publicada/).uncheck()
    await page.getByRole('button', { name: 'Salvar', exact: true }).click()
    await expect(page.getByText('A página só fica acessível depois de publicada.')).toBeVisible()
    expect((await request.get(`/p/${slug}`)).status(), 'a página despublicada').toBe(404)
    await controlePositivo(page, 'a status page despublicada')
  } finally {
    await encerrar()
  }
})

// O detector abre o incidente na 3ª falha seguida (incident.Default); com o
// intervalo de 30 s, são uns 60 s depois da edição, mais o poll da aba.
const ESPERA_DO_INCIDENTE_MS = 150_000

test('alvo: editar pelo painel, ver o incidente aberto na aba Incidentes e reconhecê-lo', async ({ page, request, credenciais }, info) => {
  test.setTimeout(240_000)
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const marca = Date.now()
    const nome = `E2E incidente ${marca}`
    const nomeNovo = `${nome} editado`
    const cabecalhos = await entrarPelaApi(request, credenciais.email, credenciais.senha)
    const id = await criarAlvo(request, cabecalhos, nome)
    await request.post('/api/v1/auth/logout', { headers: MUTACAO })

    // 1. Editar: o formulário abre com o que está gravado; o F5 prova que o
    //    detalhe vem do servidor.
    await entrarPeloPainel(page, credenciais.email, credenciais.senha)
    await page.goto(`/targets/${id}`)
    await expect(page.getByRole('heading', { name: nome })).toBeVisible()
    await page.getByRole('link', { name: 'Editar' }).click()
    await expect(page.getByLabel('Nome', { exact: true })).toHaveValue(nome)
    await page.getByLabel('Nome', { exact: true }).fill(nomeNovo)
    await page.getByLabel('Intervalo', { exact: true }).selectOption({ label: '30 segundos' })
    await page.getByRole('button', { name: 'Salvar' }).click()
    await expect(page.getByRole('heading', { name: nomeNovo })).toBeVisible()
    await page.reload()
    await expect(page.getByRole('heading', { name: nomeNovo })).toBeVisible()
    await expect(page.getByText(/a cada 30 s$/)).toBeVisible()

    // 2. O incidente abre sozinho e aparece em Abertos, à espera de
    //    reconhecimento. A aba se atualiza pelo próprio poll.
    await aba(page, /^Incidentes/).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Incidentes' })).toBeVisible()
    const cartao = page.getByRole('listitem').filter({ has: page.getByRole('link', { name: nomeNovo, exact: true }) })
    await expect(cartao.getByText('Em andamento')).toBeVisible({ timeout: ESPERA_DO_INCIDENTE_MS })

    // 3. Reconhecer para os lembretes e não resolve: o cartão segue aberto,
    //    agora reconhecido, também depois do F5.
    await cartao.getByRole('button', { name: `Reconhecer o incidente de ${nomeNovo}` }).click()
    await expect(cartao.getByText('Reconhecido', { exact: true })).toBeVisible()
    await expect(cartao.getByText(/^Reconhecido em .* · lembretes parados$/)).toBeVisible()
    await page.reload()
    await expect(cartao.getByText(/^Reconhecido em .* · lembretes parados$/)).toBeVisible()
    await expect(cartao.getByRole('button', { name: /^Reconhecer/ })).toHaveCount(0)
    await controlePositivo(page, 'o incidente reconhecido')
  } finally {
    await encerrar()
  }
})

// A pilha local não tem Resend nem um webhook de verdade, e o cadastro de um
// canal manda a mensagem de teste na hora: o caminho feliz não cabe aqui sem
// rede externa. O que cabe: as duas recusas do servidor chegam ao diálogo, e
// nenhum canal nasce delas. Cada envio gasta uma ficha do balde de envio
// (5 de burst, 1 a cada 6 min).
test('canais: o cadastro mostra as recusas do servidor e não cria canal', async ({ page, request, credenciais }, info) => {
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const cabecalhos = await entrarPelaApi(request, credenciais.email, credenciais.senha)
    const contar = async () => {
      const r = await request.get('/api/v1/channels', { headers: cabecalhos })
      expect(r.status(), 'listar os canais').toBe(200)
      return ((await r.json()) as { items: unknown[] }).items.length
    }
    const antes = await contar()

    await entrarPeloPainel(page, credenciais.email, credenciais.senha)
    await aba(page, 'Canais').click()
    await page.getByRole('button', { name: 'Novo canal' }).click()
    const dialogo = page.getByRole('dialog', { name: 'Novo canal' })

    // E-mail sem envio configurado: o servidor recusa o tipo.
    await dialogo.getByRole('radio', { name: 'E-mail' }).check()
    await dialogo.getByRole('textbox', { name: 'E-mail' }).fill('avisos@stillon-e2e.invalid')
    await dialogo.getByRole('button', { name: 'Cadastrar canal' }).click()
    await expect(dialogo.getByText('O envio de e-mail não está configurado neste servidor.')).toBeVisible()

    // Uma URL que não é de webhook do Discord: recusada antes de qualquer envio.
    await dialogo.getByRole('radio', { name: 'Discord' }).check()
    await dialogo.getByLabel('URL do webhook').fill('https://stillon-e2e.invalid/api/webhooks/1/x')
    await dialogo.getByRole('button', { name: 'Cadastrar canal' }).click()
    await expect(dialogo.getByText('Use a URL do webhook do Discord (https://discord.com/api/webhooks/…).')).toBeVisible()

    await page.keyboard.press('Escape')
    await expect(dialogo).toHaveCount(0)
    expect(await contar(), 'canais depois das recusas').toBe(antes)
    await request.post('/api/v1/auth/logout', { headers: MUTACAO })
    await controlePositivo(page, 'as recusas do cadastro de canal')
  } finally {
    await encerrar()
  }
})

// O StillOn tem um usuário só, o das outras specs. A troca de senha o deixa
// com outra senha até o fim do teste; o afterAll a devolve pelo CLI (stillon
// user set-password) mesmo com o teste vermelho, como o troca.spec.ts faz com
// o 2FA. Sem como rodar o CLI, o teste é pulado.
let senhaTrocada = false

test.afterAll(() => {
  if (senhaTrocada && podeRedefinirSenha()) redefinirSenha(process.env.E2E_EMAIL!, process.env.E2E_PASSWORD!)
  senhaTrocada = false
})

test('senha: login errado recusado, trocar pelo painel, a antiga deixa de entrar e voltar à original', async ({ page, credenciais }, info) => {
  test.skip(!podeRedefinirSenha(), 'sem como devolver a senha pelo CLI: o teste troca a senha do único usuário')
  test.setTimeout(180_000)
  const encerrar = await cadenciarRecursos(page, performance.now() + info.timeout)
  try {
    const nova = `${credenciais.senha} trocada pelo E2E`
    const entrarComSenhaErrada = async (senha: string) => {
      await page.goto('/login')
      await enviarLogin(page, credenciais.email, senha, page.getByText('E-mail ou senha incorretos.'))
      await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
    }
    const trocar = async (atual: string, outra: string) => {
      await aba(page, 'Conta').click()
      await page.getByLabel('Senha atual', { exact: true }).fill(atual)
      await page.getByLabel('Senha nova', { exact: true }).fill(outra)
      await page.getByLabel('Confirme a senha nova', { exact: true }).fill(outra)
      await enviarNoLimite(page, '/api/v1/auth/password', () => page.getByRole('button', { name: 'Trocar a senha' }).click(), page.getByText(/^Senha alterada\./))
    }

    // 1. Uma senha errada não entra e diz por quê, sem dizer qual campo errou.
    await entrarComSenhaErrada(`${credenciais.senha}x`)

    // 2. Trocar pelo painel. A partir daqui a senha do usuário é a nova.
    await entrarPeloPainel(page, credenciais.email, credenciais.senha)
    senhaTrocada = true
    await trocar(credenciais.senha, nova)

    // 3. Sair: a antiga não entra mais, a nova entra.
    await page.getByRole('button', { name: 'Sair' }).click()
    await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
    await entrarComSenhaErrada(credenciais.senha)
    await entrarPeloPainel(page, credenciais.email, nova)

    // 4. Voltar à original pelo mesmo caminho.
    await trocar(nova, credenciais.senha)
    senhaTrocada = false
    await page.getByRole('button', { name: 'Sair' }).click()
    await entrarPeloPainel(page, credenciais.email, credenciais.senha)
    await controlePositivo(page, 'a troca de senha')
  } finally {
    await encerrar()
  }
})
