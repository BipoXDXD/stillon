import AxeBuilder from '@axe-core/playwright'
import type { APIRequestContext, APIResponse } from '@playwright/test'
import { controlePositivo, expect, test } from './fixtures'

// A página pública redesenhada (PR 7 do redesign) contra o binário de
// verdade: o template Go, a CSP com font-src 'self' e as fontes de /fonts/,
// que o painel serve no mesmo host. A API monta um alvo e a status page do
// usuário (há uma por usuário: a que existir é alterada e, no fim, volta a
// ser o que era). O alvo é apagado no fim.
// Sabotagem: tirar o font-src 'self' de politicaDaPagina (publicpage.go) e
// refazer o binário → as fontes ficam em "error" e a CSP acusa a violação.

const MUTACAO = { 'X-Requested-With': 'stillon' }
// 100 caracteres sem espaço: o maior título que o servidor aceita e o pior
// caso de quebra de linha no celular (Review Focus 3 da parte 3).
const TITULO = `${'Organizacao'.repeat(9)}X`
const NOME = 'API de pagamentos'
// O estado do alvo muda enquanto o sondador trabalha: qualquer frase de um
// serviço vale.
const FRASES = /^(Todos os sistemas funcionando|1 serviço fora do ar|Instabilidade em 1 serviço|Sem dados recentes de 1 serviço)\.$/
// O login divide o balde do IP (5 de burst) com o fluxo.spec.ts, que roda
// antes: um 429 espera e tenta de novo, com teto.
const RECUOS_DO_429_MS = [2_000, 4_000, 8_000, 16_000]

// Alturas da barra do serviço e da legenda, em px. O alvo é novo: os dias
// antigos da barra não têm check, e o primeiro é sempre "sem dados". A
// legenda segue a ordem no ar, instável, fora do ar, sem dados. (A largura
// igual dos dias, sem border, é do TestPublicPageDaysHaveTheirOwnShape: aqui
// a barra é quase toda sem dados, e uma diferença de largura não apareceria.)
type Medidas = { alturaDaBarra: number; semDados: number; legenda: number[] }

function medir(): Medidas {
  const barra = document.querySelector('.s ol')
  if (!barra) throw new Error('a página não tem a barra do serviço')
  return {
    alturaDaBarra: barra.getBoundingClientRect().height,
    semDados: barra.querySelector('li')?.getBoundingClientRect().height ?? Number.NaN,
    legenda: [...document.querySelectorAll('.l > li > ol > li')].map((li) => li.getBoundingClientRect().height),
  }
}

type Pagina = {
  id: string
  slug: string
  title: string
  published: boolean
  items: { target_id: string; display_name: string }[]
}

async function entrar(request: APIRequestContext, email: string, senha: string): Promise<Record<string, string>> {
  for (let i = 0; ; i++) {
    const r = await request.post('/api/v1/auth/login', { headers: MUTACAO, data: { email, password: senha } })
    const espera = RECUOS_DO_429_MS[i]
    if (r.status() !== 429 || espera === undefined) {
      expect(r.status(), 'login pela API').toBe(200)
      const { access_token: token } = (await r.json()) as { access_token: string }
      return { Authorization: `Bearer ${token}`, ...MUTACAO }
    }
    await new Promise((ok) => setTimeout(ok, espera))
  }
}

// A limpeza é uma pilha de desfazer: cada recurso entra nela logo depois de
// existir, o último a entrar sai primeiro, e o que não chegou a existir não
// tem o que desfazer. limpar nunca lança, e uma etapa que falha não impede as
// seguintes: no finally, uma exceção trocaria o erro do teste pelo da limpeza.
// O contexto é novo porque o corpo pode ficar 60 s só no navegador, o
// IdleTimeout do servidor: a conexão ociosa do fixture request pode estar
// fechando quando a limpeza a reaproveita, o que suspeitamos ser a origem de
// um ECONNRESET visto numa rodada com a máquina carregada.
type Etapa = { nome: string; fazer: (api: APIRequestContext) => Promise<APIResponse> }

async function limpar(abrir: () => Promise<APIRequestContext>, etapas: readonly Etapa[]): Promise<string[]> {
  const falhas: string[] = []
  try {
    const api = await abrir()
    for (const { nome, fazer } of etapas) {
      try {
        const r = await fazer(api)
        if (!r.ok()) falhas.push(`${nome}: HTTP ${r.status()}`)
      } catch (e) {
        falhas.push(`${nome}: ${String(e)}`)
      }
    }
    await api.dispose()
  } catch (e) {
    falhas.push(`contexto da limpeza: ${String(e)}`)
  }
  return falhas
}

test('página pública: faixa, legenda, fontes sob a CSP, título longo a 400 px e os dois temas', async ({
  page,
  request,
  credenciais,
  playwright,
  baseURL,
}) => {
  test.setTimeout(120_000)
  const cabecalhos = await entrar(request, credenciais.email, credenciais.senha)
  const limpeza: Etapa[] = [{ nome: 'encerrar a sessão', fazer: (api) => api.post('/api/v1/auth/logout', { headers: MUTACAO }) }]
  try {
    const criado = await request.post('/api/v1/targets', {
      headers: cabecalhos,
      data: {
        name: `E2E pública ${Date.now()}`,
        url: 'https://stillon-e2e.invalid/',
        method: 'GET',
        interval_sec: 60,
        timeout_ms: 5000,
        expected_status: 200,
      },
    })
    expect(criado.status(), 'criar o alvo').toBe(201)
    const { id: alvo } = (await criado.json()) as { id: string }
    limpeza.unshift({ nome: 'apagar o alvo', fazer: (api) => api.delete(`/api/v1/targets/${alvo}`, { headers: cabecalhos }) })

    const lista = await request.get('/api/v1/status-pages', { headers: cabecalhos })
    expect(lista.status(), 'listar as status pages').toBe(200)
    const original = ((await lista.json()) as { items: Pagina[] }).items[0]
    const corpo = { title: TITULO, published: true, items: [{ target_id: alvo, display_name: NOME }] }
    // Antes do PATCH: um PATCH que estoura o prazo pode ter valido no servidor.
    if (original) {
      limpeza.unshift({
        nome: 'restaurar a status page',
        fazer: (api) =>
          api.patch(`/api/v1/status-pages/${original.id}`, {
            headers: cabecalhos,
            data: { title: original.title, published: original.published, items: original.items },
          }),
      })
    }
    const salva = original
      ? await request.patch(`/api/v1/status-pages/${original.id}`, { headers: cabecalhos, data: corpo })
      : await request.post('/api/v1/status-pages', { headers: cabecalhos, data: { ...corpo, slug: 'e2e-pagina-publica' } })
    expect(salva.status(), 'salvar a status page').toBe(original ? 200 : 201)
    const nossa = (await salva.json()) as Pagina
    // A API não apaga status page: a criada aqui sai do ar, vazia, e a próxima
    // rodada a reaproveita como original.
    if (!original) {
      limpeza.unshift({
        nome: 'despublicar a status page',
        fazer: (api) =>
          api.patch(`/api/v1/status-pages/${nossa.id}`, { headers: cabecalhos, data: { published: false, items: [] } }),
      })
    }

    await page.setViewportSize({ width: 400, height: 800 })
    for (const [tema, fundo] of [
      ['light', 'oklch(0.98 0.008 85)'],
      ['dark', 'oklch(0.2 0.01 60)'],
    ] as const) {
      await page.emulateMedia({ colorScheme: tema })
      await page.goto(`/p/${nossa.slug}`)
      await expect(page.getByRole('heading', { level: 1, name: TITULO })).toBeVisible()
      await expect(page.getByText(FRASES)).toBeVisible()
      await expect(page.getByText(NOME, { exact: true })).toBeVisible()
      await expect(page.getByText('Fora do ar (menos de 99%)')).toBeVisible()
      await expect(page.getByText('Monitorado pelo StillOn')).toBeVisible()
      expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor), `${tema}: o fundo`).toBe(fundo)
      expect(await page.evaluate(() => document.documentElement.scrollWidth), `${tema}: overflow`).toBe(400)
      await expect
        .poll(
          () =>
            page.evaluate(() => {
              const fontes: string[] = []
              document.fonts.forEach((f) => fontes.push(`${f.family.replaceAll('"', '')} ${f.status}`))
              return fontes.sort()
            }),
          `${tema}: as fontes de /fonts/ carregam sob a CSP`,
        )
        .toEqual(['Fraunces loaded', 'Inter loaded'])
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${tema}: ${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
      await controlePositivo(page, `página pública, ${tema}`)

      // A 400 px cada dia tem ~2,7 px, e um contorno fecharia o miolo: o sem
      // dados é um traço baixo, bem mais baixo que o dia no ar (WCAG 1.4.1).
      const estreita = await page.evaluate(medir)
      expect(estreita.semDados, `${tema}, 400 px: o dia sem dados na barra`).toBeLessThan(estreita.alturaDaBarra / 4)
      const [noAr = 0, , , semDadosNaLegenda = Number.NaN] = estreita.legenda
      expect(semDadosNaLegenda, `${tema}, 400 px: o sem dados da legenda`).toBeLessThan(noAr / 4)
    }

    // Na tela larga, o sem dados é o contorno da spec: altura cheia.
    await page.setViewportSize({ width: 1280, height: 800 })
    const larga = await page.evaluate(medir)
    expect(Math.abs(larga.semDados - larga.alturaDaBarra), '1280 px: o dia sem dados tem a altura da barra').toBeLessThan(0.5)
    const [noArLarga = 0, , , semDadosLarga = Number.NaN] = larga.legenda
    expect(Math.abs(semDadosLarga - noArLarga), '1280 px: o sem dados da legenda tem a altura do no ar').toBeLessThan(0.5)
  } finally {
    // soft: registra a falha sem lançar. Com o corpo do teste verde, a limpeza
    // que falhou reprova o teste; com o corpo vermelho, soma-se ao erro dele.
    const abrir = async () => playwright.request.newContext({ baseURL, storageState: await request.storageState() })
    expect.soft(await limpar(abrir, limpeza), 'a limpeza').toEqual([])
  }
})
