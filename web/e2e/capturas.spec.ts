import { mkdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import type { Page } from '@playwright/test'
import { incidente } from '../src/test/fixtures'
import { imitarApi } from './api-imitada'
import { expect, test } from './fixtures'

// As capturas "depois" do redesign (PR 8), no formato das de
// docs/design/painel-antes: <tela>-<largura>-<tema>.png, página inteira, a
// 400 e 1280 px, nos temas claro e escuro. A API é a imitada do
// a11y.spec.ts (sem segredo, sem backend de dados); a página pública é o
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

// preparar registra rotas próprias da tela; o page.route devolve um
// Disposable que o roteiro não usa (o unrouteAll limpa tudo a cada volta).
type Tela = { nome: string; rota: string; preparar?: (page: Page) => Promise<unknown> }

const erroDaApi = { error: { code: 'internal', message: '' }, trace_id: 't' }
const ontem = Date.now() - 26 * 3_600_000
const incidenteResolvidoDaApi = incidente({
  id: 'inc-api',
  target_id: 'a1',
  target_name: 'API',
  started_at: new Date(ontem).toISOString(),
  resolved_at: new Date(ontem + 25 * 60_000).toISOString(),
  cause: 'timeout',
})

const TELAS: Tela[] = [
  { nome: 'painel', rota: '/' },
  {
    nome: 'painel-vazio',
    rota: '/',
    preparar: (page) => page.route('**/api/v1/targets/summary', (r) => r.fulfill({ status: 200, json: { items: [] } })),
  },
  {
    nome: 'painel-erro',
    rota: '/',
    preparar: (page) => page.route('**/api/v1/targets/summary', (r) => r.fulfill({ status: 500, json: erroDaApi })),
  },
  {
    nome: 'detalhe-alvo',
    rota: '/targets/a1',
    // A API imitada devolve a mesma lista de incidentes a qualquer filtro, e
    // o detalhe da API (no ar) mostraria o incidente aberto do Banco.
    preparar: (page) =>
      page.route(
        (u) => u.pathname === '/api/v1/incidents' && u.searchParams.get('target_id') === 'a1',
        (r) => r.fulfill({ status: 200, json: { items: [incidenteResolvidoDaApi] } }),
      ),
  },
  { nome: 'incidentes', rota: '/incidents' },
  { nome: 'canais', rota: '/channels' },
  { nome: 'status-page', rota: '/status-pages' },
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
        await fotografar(page, largura, `${tela.nome}-${largura}-${tema}.png`)
      }
    }
  }
})

test('capturas da página pública', async ({ page }) => {
  const exemplo = process.env.STILLON_EXEMPLO_PUBLICA ?? ''
  test.skip(
    exemplo === '',
    'gere antes o exemplo: STILLON_EXEMPLO_PUBLICA=<arquivo absoluto> go test ./internal/httpapi -run TestGerarExemploDaPaginaPublica',
  )
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
