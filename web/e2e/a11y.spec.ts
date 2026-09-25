import AxeBuilder from '@axe-core/playwright'
import type { Page, Route } from '@playwright/test'
import type { z } from 'zod'
import type { ConfirmacaoTotp, InicioDaTroca, Serie } from '../src/lib/api/schemas'
import { alvo, canal, incidente, itemDoResumo, me, pagina, sessao } from '../src/test/fixtures'
import { controlePositivo, expect, test, violacoes } from './fixtures'

// Todas as rotas do painel, com a API respondida pelo próprio teste
// (page.route): os cinco estados do dashboard, um canal falhando, um canal
// pausado, sessões.
// Para cada rota, nos dois temas: axe (WCAG 2 A e AA, contraste incluído)
// sem violações, e zero violações de CSP. É o que dá cobertura de CSP a
// TODAS as telas, não só às do fluxo crítico. O axe roda sob a CSP de
// produção, sem bypassCSP (spike do Plano 6, §8.4).

// As respostas vêm das MESMAS fixtures tipadas dos testes de tela
// (src/test/fixtures.ts, z.input dos schemas): uma resposta fora do contrato
// não compila aqui também (spec do Plano 6 §7.4).
const agora = new Date().toISOString()
const hora = new Date(Math.floor(Date.now() / 3_600_000) * 3_600_000 - 3_600_000).toISOString()
const spark = [{ bucket: hora, total: 60, ok: 60, p50_ms: 110 }]
const item = (id: string, name: string, state: 'up' | 'degraded' | 'down' | 'unknown', enabled = true) =>
  itemDoResumo({
    id,
    name,
    enabled,
    state,
    last_checked_at: agora,
    last_latency_ms: state === 'down' ? null : 120,
    uptime_24h: 0.9993,
    open_incident_id: state === 'down' ? 'inc-1' : null,
    spark,
  })
const a1 = alvo({ id: 'a1', header_names: ['Authorization'], created_at: agora })
const bruta: z.input<typeof Serie> = {
  resolution: 'raw',
  items: [
    { checked_at: agora, status: 'up', latency_ms: 120, status_code: 200, error_kind: null },
    { checked_at: agora, status: 'down', latency_ms: 10000, status_code: null, error_kind: 'timeout' },
  ],
}
// A janela de 7 dias vem por hora (o corte de 48 h é do servidor): as barras
// de uptime da hora aparecem (spec do Plano 6 §4.4).
const horaria: z.input<typeof Serie> = {
  resolution: '1h',
  items: [
    { bucket: hora, total: 60, ok: 57, p50_ms: 110, p95_ms: 300, p99_ms: 400, max_ms: 500 },
    { bucket: agora, total: 30, ok: 30, p50_ms: 100, p95_ms: 200, p99_ms: 250, max_ms: 300 },
  ],
}
const respostas: Record<string, unknown> = {
  'POST /auth/refresh': { access_token: 'token-de-teste', expires_in: 900 },
  'GET /me': me({ created_at: agora }),
  'GET /targets/summary': {
    items: [
      item('a1', 'API', 'up'), item('a2', 'Site', 'degraded'), item('a3', 'Banco', 'down'),
      item('a4', 'Novo', 'unknown'), item('a5', 'Parado', 'unknown', false),
    ],
  },
  'GET /targets': { items: [a1] },
  'GET /targets/a1': a1,
  'GET /incidents': {
    items: [incidente({ id: 'inc-1', target_id: 'a3', target_name: 'Banco', started_at: agora, cause: 'conn_refused' })],
  },
  'GET /channels': {
    items: [
      canal({ id: 'c1', created_at: agora }),
      canal({ id: 'c2', hint: 'discord.com/…/zzzz', status: 'failing', created_at: agora, last_error: 'invalid_channel', last_failure_at: agora }),
      canal({ id: 'c3', kind: 'email', hint: 'd***@exemplo.com', status: 'pending', created_at: agora }),
      // Pausado (spec do Plano 9 §4.1): a linha "Pausado" e o "Retomar".
      canal({ id: 'c4', hint: 'discord.com/…/pppp', enabled: false, created_at: agora }),
    ],
  },
  'GET /status-pages': { items: [pagina({ id: 'p1', items: [{ target_id: 'a1', display_name: 'API' }], created_at: agora })] },
  'GET /sessions': { items: [sessao({ id: 'f1', created_at: agora, last_used_at: agora })] },
}

// A troca de autenticador (spec do Plano 9 §4.2): as respostas do rotate e
// do rotate/confirm, no formato do enroll (mais o rotation, A7 do Plano 10)
// e do confirm.
const SEGREDO_NOVO = 'KRSXG5CTMVRXEZLU'
const troca: z.input<typeof InicioDaTroca> = {
  otpauth_uri: `otpauth://totp/Vigilume:dono%40exemplo.com?secret=${SEGREDO_NOVO}&issuer=Vigilume`,
  rotation: '0123456789abcdef0123456789abcdef',
}
const codigosNovos: z.input<typeof ConfirmacaoTotp> = {
  recovery_codes: Array.from({ length: 10 }, (_, i) => `NOVO${String(i).padStart(2, '0')}CODIGO000${i}`),
}
respostas['POST /auth/totp/rotate'] = troca
respostas['POST /auth/totp/rotate/confirm'] = codigosNovos

function respostaDe(metodo: string, u: URL): unknown {
  const chave = `${metodo} ${u.pathname.replace('/api/v1', '')}`
  if (chave === 'GET /targets/a1/checks') {
    const desde = Date.parse(u.searchParams.get('from') ?? '')
    return Date.now() - desde > 48 * 3_600_000 ? horaria : bruta
  }
  return respostas[chave]
}

async function imitarApi(page: Page) {
  await page.route('**/api/v1/**', (route: Route) => {
    const corpo = respostaDe(route.request().method(), new URL(route.request().url()))
    return corpo === undefined
      ? route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '' }, trace_id: 't' } })
      : route.fulfill({ status: 200, json: corpo })
  })
}

const ROTAS: [string, string][] = [
  ['/', 'Painel'],
  ['/targets/new', 'Novo alvo'],
  ['/targets/a1', 'API'],
  ['/targets/a1?janela=7d', 'API'],
  ['/targets/a1/edit', 'Editar alvo'],
  ['/incidents', 'Incidentes'],
  ['/channels', 'Canais de notificação'],
  ['/status-pages', 'Status page'],
  ['/settings', 'Configurações'],
]

for (const tema of ['claro', 'escuro'] as const) {
  test(`acessibilidade e CSP em todas as rotas, tema ${tema}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
    await imitarApi(page)
    // A tela de login, sem sessão, tem o teste próprio no fim do arquivo.
    for (const [rota, titulo] of ROTAS) {
      await page.goto(rota)
      await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
      await expect(page.locator('html')).toHaveClass(tema === 'escuro' ? /dark/ : /^(?!.*dark)/)
      if (rota.endsWith('janela=7d')) {
        // As barras de uptime da hora desenham, com cor (variável CSS).
        const barra = page.getByTestId('grafico-latencia').locator('.recharts-bar-rectangle path').first()
        await expect(barra).toBeAttached()
        expect(await barra.evaluate((e) => getComputedStyle(e).fill)).toMatch(/^rgb/)
      }
      const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(r.violations.map((v) => `${rota}: ${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
      await expect.poll(() => violacoes(page), rota).toEqual([])
    }
    // Um diálogo aberto também passa pelo axe e pela CSP.
    await page.goto('/channels')
    await page.getByRole('button', { name: 'Novo canal' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(r.violations.map((v) => v.id)).toEqual([])
    expect(violacoes(page)).toEqual([])
    // A confirmação de pausar (spec do Plano 9 §4.1), e a linha do pausado.
    await page.keyboard.press('Escape')
    await expect(page.getByText('⏸ Pausado: os avisos são descartados')).toBeVisible()
    await page.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }).click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    const pausar = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(pausar.violations.map((v) => v.id)).toEqual([])
    expect(violacoes(page)).toEqual([])
    // O aviso de trocar o destino de um alvo com headers (B1 do Plano 10).
    await page.goto('/targets/a1/edit')
    await page.getByLabel('URL').fill('https://outro.exemplo.com/health')
    await page.getByRole('button', { name: 'Salvar' }).click()
    await expect(page.getByRole('alertdialog', { name: 'Trocar o destino do alvo?' })).toBeVisible()
    const destino = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(destino.violations.map((v) => v.id)).toEqual([])
    expect(violacoes(page)).toEqual([])
    await controlePositivo(page)
  })
}

// Os três passos do diálogo de troca de autenticador (spec do Plano 9
// §4.3), com o 2FA ligado no /me: axe e CSP em cada passo, nos dois temas.
// O passo 2 tem o QR (SVG) do app novo; o 3, os códigos.
for (const tema of ['claro', 'escuro'] as const) {
  test(`troca de autenticador: axe e CSP nos três passos, tema ${tema}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
    await imitarApi(page)
    // Registrada depois, esta rota vence a do imitarApi para o /me.
    await page.route('**/api/v1/me', (route) =>
      route.fulfill({ status: 200, json: me({ totp_enabled: true, created_at: agora }) }),
    )
    const passar = async (passo: string) => {
      const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(r.violations.map((v) => `${passo}: ${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
      await expect.poll(() => violacoes(page), passo).toEqual([])
    }
    await page.goto('/settings')
    await page.getByRole('button', { name: 'Trocar autenticador' }).click()
    const dialogo = page.getByRole('dialog', { name: 'Trocar o autenticador' })
    await expect(dialogo.getByLabel('Sua senha')).toBeVisible()
    await passar('passo 1')
    await dialogo.getByLabel('Sua senha').fill('uma senha bem comprida')
    await dialogo.getByLabel('Código do autenticador').fill('123456')
    await dialogo.getByRole('button', { name: 'Continuar' }).click()
    await expect(dialogo.getByText(SEGREDO_NOVO)).toBeVisible()
    // O <title> do SVG não é o atributo title que o getByTitle procura.
    await expect(dialogo.locator('svg', { has: page.locator('title', { hasText: 'QR code do autenticador' }) })).toBeVisible()
    await passar('passo 2')
    await dialogo.getByLabel('Código de 6 dígitos').fill('654321')
    await dialogo.getByRole('button', { name: 'Confirmar a troca' }).click()
    await expect(dialogo.getByText(codigosNovos.recovery_codes[0]!)).toBeVisible()
    await passar('passo 3')
    await dialogo.getByLabel('Guardei os códigos').check()
    await dialogo.getByRole('button', { name: 'Concluir' }).click()
    await expect(dialogo).toBeHidden()
    // Dentro da região dos avisos: o Radix Toast copia o texto para uma região
    // de anúncio oculta por um instante, e um getByText na página inteira
    // acharia os dois (o modo estrito do Playwright recusa, conforme o tempo).
    await expect(
      page.getByRole('region', { name: /Notifications/ }).getByText('Autenticador trocado. As outras sessões foram encerradas.'),
    ).toBeVisible()
    await controlePositivo(page)
  })
}

// A passada de teclado da spec §6, repetível: em cada rota, numa carga
// nova, o primeiro Tab é o "Pular para o conteúdo" (§4.9), e ativá-lo leva o
// Tab seguinte para dentro do conteúdo; depois, Tab por todas as paradas até
// o foco sair da página — nenhuma armadilha — com o foco visível (outline do
// :focus-visible) em cada uma. As paradas vão para o relatório do teste.
// O <h1> aparece antes dos dados: enquanto a consulta voa, o <main> tem só
// o título (tabIndex -1, fora da ordem do Tab) e o esqueleto, sem nada
// focável, e o Tab depois do salto sai da página. Isso é a tela carregando,
// não o salto quebrado; esperar o esqueleto sumir (o "Carregando…" do
// EstadoDaConsulta, aria-busy) tira a corrida sem afrouxar o teste: com o
// conteúdo de pé, um salto que não move o foco ainda leva o Tab ao cabeçalho.
// Sem o --trace do config, a corrida perdia em ~6 de 10 execuções.
async function esperarOConteudo(page: Page, rota: string) {
  await expect(page.locator('#conteudo [aria-busy="true"]'), `${rota}: carregando`).toHaveCount(0)
  await expect(page.getByText('Carregando…', { exact: true }), `${rota}: carregando`).toHaveCount(0)
}

test('passada de teclado em todas as rotas', async ({ page }, info) => {
  await imitarApi(page)
  const relatorio: string[] = []
  for (const [rota, titulo] of ROTAS) {
    await page.goto(rota)
    await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
    await esperarOConteudo(page, rota)
    await page.keyboard.press('Tab')
    await expect(page.getByRole('link', { name: 'Pular para o conteúdo' }), rota).toBeFocused()
    await page.keyboard.press('Enter')
    await page.keyboard.press('Tab')
    expect(await page.evaluate(() => !!document.activeElement?.closest('#conteudo')), `${rota}: depois do salto`).toBe(true)

    await page.goto(rota)
    await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
    // Aqui também: sem isto a passada podia percorrer só o cabeçalho (8
    // paradas, o mínimo abaixo) e nunca o conteúdo da tela.
    await esperarOConteudo(page, rota)
    const paradas: string[] = []
    for (let i = 0; ; i++) {
      expect(i, `${rota}: o foco não sai da página em 150 Tabs (armadilha?)`).toBeLessThan(150)
      await page.keyboard.press('Tab')
      const p = await page.evaluate(() => {
        const e = document.activeElement as HTMLElement | null
        if (!e || e === document.body) return null
        const cs = getComputedStyle(e)
        const rotulo = (e as HTMLInputElement).labels?.[0]?.textContent
        const nome = (e.getAttribute('aria-label') ?? rotulo ?? e.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 30)
        // Visível: um contorno com largura E com cor que não é transparente
        // ("outline: 2px solid transparent" tem estilo e largura, e não se vê).
        // O alfa é o 4º número de rgba()/color(); rgb() e hex não têm alfa.
        const numeros = cs.outlineColor.match(/[\d.]+/g) ?? []
        const alfa = cs.outlineColor === 'transparent' ? 0 : numeros.length >= 4 ? Number(numeros[3]) : 1
        return {
          nome: `${e.tagName.toLowerCase()} "${nome}"`,
          visivel: cs.outlineStyle !== 'none' && cs.outlineWidth !== '0px' && alfa > 0,
          contorno: `${cs.outlineStyle} ${cs.outlineWidth} ${cs.outlineColor}`,
        }
      })
      if (!p) break
      expect(p.visivel, `${rota}: foco invisível em ${p.nome} (${p.contorno})`).toBe(true)
      paradas.push(p.nome)
    }
    // Sem isto, um laço que parasse no primeiro Tab (foco no <body>) passaria
    // sem conferir parada nenhuma: a primeira é o salto, e há o cabeçalho
    // inteiro (salto, cinco links, tema, Sair) antes do conteúdo.
    expect(paradas[0], `${rota}: a primeira parada`).toBe('a "Pular para o conteúdo"')
    expect(paradas.length, `${rota}: paradas`).toBeGreaterThanOrEqual(8)
    relatorio.push(`${rota}: ${paradas.length} paradas — ${paradas.join(' | ')}`)
  }
  await info.attach('passada-de-teclado.txt', { body: relatorio.join('\n'), contentType: 'text/plain' })
})

test('a tela de login, sem sessão', async ({ page }) => {
  await page.route('**/api/v1/auth/refresh', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
  )
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Entrar no Vigilume' })).toBeVisible()
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  expect(r.violations.map((v) => v.id)).toEqual([])
  expect(violacoes(page)).toEqual([])
})
