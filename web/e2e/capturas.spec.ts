import { mkdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import type { Page } from '@playwright/test'
import type { z } from 'zod'
import type { Check, Hora, HoraDoSpark, Serie } from '../src/lib/api/schemas'
import { alvo, incidente, itemDoResumo, pagina } from '../src/test/fixtures'
import { imitarApi } from './api-imitada'
import { expect, test } from './fixtures'

// As capturas "depois" do redesign (PR 8), no formato das de
// docs/design/painel-antes: <tela>-<largura>-<tema>.png, página inteira, a
// 400 e 1280 px, nos temas claro e escuro. A API é a imitada do
// a11y.spec.ts (sem segredo, sem backend de dados), com dados de exemplo
// próprios nas telas que os mostram (preparar); a página pública é o
// template Go renderizado com dados de exemplo (STILLON_EXEMPLO_PUBLICA,
// gerado por TestGerarExemploDaPaginaPublica), e as fontes vêm do binário.
// Só roda com CAPTURAR=1: as imagens vão para o repositório, não para o CI.
// Para gerar (na raiz, com o Docker de pé):
//   mkdir -p web/e2e/.tmp
//   STILLON_EXEMPLO_PUBLICA="$PWD/web/e2e/.tmp/status-publica.html" go test ./internal/httpapi/ -run TestGerarExemploDaPaginaPublica
//   cd web && npm run build
//   CAPTURAR=1 STILLON_EXEMPLO_PUBLICA="$PWD/e2e/.tmp/status-publica.html" npx playwright test e2e/capturas.spec.ts
test.skip(!process.env.CAPTURAR, 'capturas só com CAPTURAR=1 (PR 8 do redesign)')

const DESTINO = path.resolve(import.meta.dirname, '../../docs/design/painel-depois')
const LARGURAS = [400, 1280] as const
const ALTURA = 900
const TEMAS = [
  ['claro', 'light'],
  ['escuro', 'dark'],
] as const

// preparar registra as respostas próprias da tela (o page.route devolve um
// Disposable que o roteiro não usa: o unrouteAll limpa tudo a cada volta).
// conferir barra a captura se os dados de exemplo não chegaram à tela.
type Tela = {
  nome: string
  rota: string
  preparar?: (page: Page) => Promise<unknown>
  conferir?: (page: Page) => Promise<void>
}

const erroDaApi = { error: { code: 'internal', message: '' }, trace_id: 't' }
// Os dados de exemplo: a API imitada do a11y.spec.ts tem o mínimo para exercitar
// as telas, e uma captura precisa de dados que não se contradigam. Tudo sai de
// uma série só da API: 30 dias de horas, a 30 s por check (120 por hora). O
// resumo, os KPIs, o gráfico e os incidentes do detalhe leem a mesma série.
const HORA_MS = 3_600_000
const CHECKS_POR_HORA = 120
const INTERVALO_DA_API_S = 30
const HORAS_EM_30_DIAS = 30 * 24
const HORAS_ATRAS_DA_QUEDA = 6
const CHECKS_FORA_NA_QUEDA = 24 // 12 minutos
const HORAS_ATRAS_DA_HORA_INSTAVEL = 14
const HORAS_ATRAS_DO_PICO = 52
const PICO_MS = 9800

const horaCheia = (horasAtras: number) => (Math.floor(Date.now() / HORA_MS) - horasAtras) * HORA_MS
const iso = (ms: number) => new Date(ms).toISOString()

function checksOkDaHora(horasAtras: number): number {
  if (horasAtras === HORAS_ATRAS_DA_QUEDA) return CHECKS_POR_HORA - CHECKS_FORA_NA_QUEDA
  return horasAtras === HORAS_ATRAS_DA_HORA_INSTAVEL ? CHECKS_POR_HORA - 1 : CHECKS_POR_HORA
}

function horaDaApi(horasAtras: number): z.input<typeof Hora> {
  const p50 = Math.round(125 + 15 * Math.sin((horasAtras * 2 * Math.PI) / 24))
  const folga = horasAtras === HORAS_ATRAS_DA_QUEDA ? 3 : 1.6
  const p95 = horasAtras === HORAS_ATRAS_DO_PICO ? PICO_MS : Math.round(p50 * folga)
  return {
    bucket: iso(horaCheia(horasAtras)),
    total: CHECKS_POR_HORA,
    ok: checksOkDaHora(horasAtras),
    p50_ms: p50,
    p95_ms: p95,
    p99_ms: p95 + 40,
    max_ms: p95 + 90,
  }
}

// Da mais antiga à mais recente, com a última hora cheia no fim.
const horasDaApi = (quantas: number) => Array.from({ length: quantas }, (_, i) => horaDaApi(quantas - i))

// A janela de 24 h vem em checks (o corte de 48 h é do servidor): cada hora
// vira os checks dela, e os fora do ar começam 10 minutos depois da hora cheia.
const PRIMEIRO_CHECK_FORA = 20

function checksDaApi(): z.input<typeof Check>[] {
  return horasDaApi(24).flatMap((h) =>
    Array.from({ length: h.total }, (_, i) => {
      const fora = i >= PRIMEIRO_CHECK_FORA && i < PRIMEIRO_CHECK_FORA + h.total - h.ok
      return {
        checked_at: iso(Date.parse(h.bucket) + (i * HORA_MS) / h.total),
        status: fora ? ('down' as const) : ('up' as const),
        latency_ms: fora ? 10000 : (h.p50_ms ?? 0) + (i % 9) * 3 - 12,
        status_code: fora ? null : 200,
        error_kind: fora ? 'timeout' : null,
      }
    }),
  )
}

function serieDaApi(janela: string | null): z.input<typeof Serie> {
  if (janela === '7d') return { resolution: '1h', items: horasDaApi(7 * 24) }
  if (janela === '30d') return { resolution: '1h', items: horasDaApi(HORAS_EM_30_DIAS) }
  return { resolution: 'raw', items: checksDaApi() }
}

const uptimeDe = (horas: readonly HoraDoSpark[]) =>
  horas.reduce((n, h) => n + h.ok, 0) / horas.reduce((n, h) => n + h.total, 0)

// As 24 horas de um alvo: todas boas, menos as de `checksFora` (horas atrás ->
// checks perdidos). Com 120 checks por hora, 1 perdido é hora degradada e 2
// ou mais, hora fora do ar (os limiares do Minigrafico).
const horasDoSpark = (p50: number, checksFora: Record<number, number> = {}): HoraDoSpark[] =>
  Array.from({ length: 24 }, (_, i) => ({
    bucket: iso(horaCheia(24 - i)),
    total: CHECKS_POR_HORA,
    ok: CHECKS_POR_HORA - (checksFora[24 - i] ?? 0),
    p50_ms: p50 + (i % 5) * 4,
  }))

function comSpark(
  id: string,
  name: string,
  state: 'up' | 'degraded' | 'down',
  latencia: number | null,
  spark: HoraDoSpark[],
) {
  return itemDoResumo({
    id,
    name,
    state,
    last_checked_at: iso(Date.now() - 20_000),
    last_latency_ms: latencia,
    uptime_24h: uptimeDe(spark),
    open_incident_id: state === 'down' ? 'inc-1' : null,
    spark,
  })
}

// O painel: Banco fora do ar desde 20 minutos antes da hora cheia, Site lento
// acima do limiar de 2000 ms, a API com a queda da série dela, um alvo que
// nunca foi sondado e um pausado.
const resumoDoPainel = () => ({
  items: [
    comSpark('a1', 'API', 'up', 128, horasDoSpark(120, { [HORAS_ATRAS_DA_QUEDA]: CHECKS_FORA_NA_QUEDA, [HORAS_ATRAS_DA_HORA_INSTAVEL]: 1 })),
    comSpark('a6', 'Autenticação', 'up', 96, horasDoSpark(90)),
    comSpark('a7', 'Fila de e-mails', 'up', 210, horasDoSpark(200, { 11: 1 })),
    comSpark('a2', 'Site', 'degraded', 2640, horasDoSpark(900, { 3: 1, 2: 1, 1: 1 })),
    comSpark('a3', 'Banco', 'down', null, horasDoSpark(80, { 9: 1, 1: 40 })),
    itemDoResumo({ id: 'a4', name: 'Novo', state: 'unknown', last_checked_at: null, last_latency_ms: null, uptime_24h: null, spark: [] }),
    itemDoResumo({
      id: 'a5',
      name: 'Parado',
      enabled: false,
      state: 'unknown',
      last_checked_at: iso(Date.now() - 3 * 24 * HORA_MS),
      last_latency_ms: null,
      uptime_24h: null,
      spark: [],
    }),
  ],
})

const incidenteDoBanco = () =>
  incidente({ id: 'inc-1', target_id: 'a3', target_name: 'Banco', started_at: iso(horaCheia(0) - 20 * 60_000), cause: 'conn_refused' })

// O incidente da queda: 12 minutos, nos checks fora do ar da série.
function incidenteDaQueda() {
  const inicio = horaCheia(HORAS_ATRAS_DA_QUEDA) + 10 * 60_000
  return incidente({
    id: 'inc-api',
    target_id: 'a1',
    target_name: 'API',
    started_at: iso(inicio),
    resolved_at: iso(inicio + 12 * 60_000),
    cause: 'timeout',
  })
}

const emIncidentes = (u: URL) => u.pathname === '/api/v1/incidents'

async function prepararPainel(page: Page) {
  await page.route('**/api/v1/targets/summary', (r) => r.fulfill({ status: 200, json: resumoDoPainel() }))
  await page.route(emIncidentes, (r) => r.fulfill({ status: 200, json: { items: [incidenteDoBanco()] } }))
}

async function prepararDetalhe(page: Page) {
  await prepararPainel(page)
  await page.route('**/api/v1/targets/a1', (r) =>
    r.fulfill({ status: 200, json: alvo({ id: 'a1', interval_sec: INTERVALO_DA_API_S, header_names: ['Authorization'] }) }),
  )
  await page.route('**/api/v1/targets/a1/checks**', (r) =>
    r.fulfill({ status: 200, json: serieDaApi(new URL(r.request().url()).searchParams.get('window')) }),
  )
  // Registrada depois da do painel, esta vence no filtro por alvo.
  await page.route(
    (u) => emIncidentes(u) && u.searchParams.get('target_id') === 'a1',
    (r) => r.fulfill({ status: 200, json: { items: [incidenteDaQueda()] } }),
  )
}

const URL_DA_PAGINA_DE_EXEMPLO = 'https://status.loja.example/p/loja-exemplo'
const paginaDeExemplo = pagina({
  id: 'p1',
  slug: 'loja-exemplo',
  title: 'Status da Loja Exemplo',
  public_url: URL_DA_PAGINA_DE_EXEMPLO,
  items: [{ target_id: 'a1', display_name: 'API de pagamentos' }],
})

const TELAS: Tela[] = [
  {
    nome: 'painel',
    rota: '/',
    preparar: prepararPainel,
    conferir: async (page) => {
      await expect(page.getByRole('img', { name: /Últimas 24 h: .*hora degradada.*hora fora do ar/ }).first()).toBeVisible()
      await expect(page.getByText('Sem dados · nunca verificado')).toBeVisible()
    },
  },
  {
    nome: 'painel-vazio',
    rota: '/',
    preparar: async (page) => {
      await page.route('**/api/v1/targets/summary', (r) => r.fulfill({ status: 200, json: { items: [] } }))
      await page.route(emIncidentes, (r) => r.fulfill({ status: 200, json: { items: [] } }))
    },
    conferir: async (page) => {
      await expect(page.getByRole('link', { name: /^Incidentes$/ }).first()).toBeVisible()
    },
  },
  {
    nome: 'painel-erro',
    rota: '/',
    preparar: (page) => page.route('**/api/v1/targets/summary', (r) => r.fulfill({ status: 500, json: erroDaApi })),
  },
  {
    // Janela de 7 dias: até 48 h o servidor responde em checks, e só a série
    // por hora tem a mediana e a faixa p50–p95.
    nome: 'detalhe-alvo',
    rota: '/targets/a1?janela=7d',
    preparar: prepararDetalhe,
    conferir: async (page) => {
      await expect(page.getByText(/^pico de 9\.800 ms em .*, fora da escala$/)).toBeVisible()
      await expect(page.getByText('p50–p95', { exact: true })).toBeVisible()
      await expect(page.getByText('Horas com menos de 99% no ar', { exact: true })).toBeVisible()
      await expect(page.getByTestId('grafico-latencia').locator('.recharts-area-area')).toBeVisible()
    },
  },
  { nome: 'incidentes', rota: '/incidents' },
  { nome: 'canais', rota: '/channels' },
  {
    nome: 'status-page',
    rota: '/status-pages',
    preparar: (page) =>
      page.route('**/api/v1/status-pages', (r) => r.fulfill({ status: 200, json: { items: [paginaDeExemplo] } })),
    conferir: async (page) => {
      await expect(page.getByText(URL_DA_PAGINA_DE_EXEMPLO)).toBeVisible()
      await expect(page.getByText('localhost')).toHaveCount(0)
    },
  },
  { nome: 'configuracoes', rota: '/settings' },
  { nome: 'novo-alvo', rota: '/targets/new' },
  {
    nome: 'login',
    rota: '/login',
    preparar: (page) =>
      page.route('**/api/v1/auth/refresh', (r) =>
        r.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
      ),
  },
]

// A tela pronta: título visível, nada carregando (o erro do painel espera as
// tentativas da consulta) e as fontes carregadas.
async function esperarATela(page: Page) {
  await expect(page.locator('h1').first()).toBeVisible()
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0, { timeout: 20_000 })
  await expect(page.getByText('Carregando…', { exact: true })).toHaveCount(0, { timeout: 20_000 })
  await page.evaluate(async () => {
    await document.fonts.ready
  })
}

// A foto da página inteira. O fullPage do Playwright estica a página sem
// mudar a janela, e a barra de navegação fixa do celular sairia no meio do
// conteúdo; com a janela da altura da página, ela fica no rodapé, como o
// usuário a vê ao rolar até o fim.
async function fotografar(page: Page, largura: number, arquivo: string) {
  const altura = await page.evaluate(() => document.documentElement.scrollHeight)
  await page.setViewportSize({ width: largura, height: Math.max(ALTURA, altura) })
  await page.screenshot({ path: path.join(DESTINO, arquivo), fullPage: true, animations: 'disabled' })
}

test('capturas das telas do painel', async ({ page }) => {
  test.setTimeout(10 * 60_000)
  mkdirSync(DESTINO, { recursive: true })
  for (const tela of TELAS) {
    for (const largura of LARGURAS) {
      for (const [tema, esquema] of TEMAS) {
        await page.unrouteAll({ behavior: 'ignoreErrors' })
        await imitarApi(page)
        // Registrada depois, a rota da tela vence a do imitarApi.
        await tela.preparar?.(page)
        await page.setViewportSize({ width: largura, height: ALTURA })
        await page.emulateMedia({ colorScheme: esquema })
        await page.goto(tela.rota)
        await esperarATela(page)
        await tela.conferir?.(page)
        await fotografar(page, largura, `${tela.nome}-${largura}-${tema}.png`)
      }
    }
  }
})

test('capturas da página pública', async ({ page }) => {
  // Sem o exemplo, erro e não pulo: as telas seriam regeradas, e as quatro
  // imagens da página pública ficariam velhas em silêncio.
  const exemplo = process.env.STILLON_EXEMPLO_PUBLICA ?? ''
  if (exemplo === '') {
    throw new Error(
      'gere antes o exemplo: STILLON_EXEMPLO_PUBLICA=<arquivo absoluto> go test ./internal/httpapi -run TestGerarExemploDaPaginaPublica',
    )
  }
  const html = readFileSync(exemplo, 'utf8')
  await page.route('**/p/exemplo', (r) => r.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: html }))
  mkdirSync(DESTINO, { recursive: true })
  for (const largura of LARGURAS) {
    for (const [tema, esquema] of TEMAS) {
      await page.setViewportSize({ width: largura, height: ALTURA })
      await page.emulateMedia({ colorScheme: esquema })
      await page.goto('/p/exemplo')
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
      await page.evaluate(async () => {
        await document.fonts.ready
      })
      await fotografar(page, largura, `status-publica-${largura}-${tema}.png`)
    }
  }
})
