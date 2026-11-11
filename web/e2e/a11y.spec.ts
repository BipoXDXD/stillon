import AxeBuilder from '@axe-core/playwright'
import type { Locator, Page } from '@playwright/test'
import type { z } from 'zod'
import type { Serie } from '../src/lib/api/schemas'
import { alvo, incidente, me, pagina } from '../src/test/fixtures'
import { agora, codigosNovos, hora, imitarApi, item, SEGREDO_NOVO, TOKEN_DE_PING } from './api-imitada'
import { controlePositivo, expect, test } from './fixtures'

// Todas as rotas do painel, com a API respondida pelo próprio teste
// (page.route): os cinco estados do dashboard, um canal falhando, um canal
// pausado, sessões.
// Para cada rota, nos dois temas: axe (WCAG 2 A e AA, contraste incluído)
// sem violações, e zero violações de CSP. É o que dá cobertura de CSP a
// TODAS as telas, não só às do fluxo crítico. O axe roda sob a CSP de
// produção, sem bypassCSP (spike do Plano 6, §8.4).

const ROTAS: [string, string][] = [
  ['/', 'Painel'],
  ['/targets/new', 'Novo alvo'],
  ['/targets/a1', 'API'],
  ['/targets/a1?janela=7d', 'API'],
  ['/targets/a1/edit', 'Editar alvo'],
  ['/heartbeats/new', 'Novo heartbeat'],
  ['/heartbeats/h1', 'Backup noturno'],
  ['/heartbeats/h1/edit', 'Editar heartbeat'],
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
        // A hora com menos de 99% no ar vira faixa hachurada (forma + cor),
        // com o padrão do próprio gráfico e o traço em variável CSS.
        const faixa = page.getByTestId('grafico-latencia').locator('.recharts-reference-area-rect').first()
        await expect(faixa).toBeAttached()
        const padrao = (await faixa.getAttribute('fill'))?.match(/^url\(#(.+)\)$/)?.[1]
        expect(padrao, 'a faixa usa o padrão de hachura').toBeTruthy()
        const traco = page.locator(`pattern[id="${padrao}"] path`)
        expect(await traco.evaluate((e) => getComputedStyle(e).stroke)).toMatch(/^(rgb|oklch)\(/)
      }
      const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(r.violations.map((v) => `${rota}: ${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
      await controlePositivo(page, rota)
    }
    // Um diálogo aberto também passa pelo axe e pela CSP.
    await page.goto('/channels')
    await page.getByRole('button', { name: 'Novo canal' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(r.violations.map((v) => v.id)).toEqual([])
    await controlePositivo(page, 'diálogo de novo canal')
    // A confirmação de pausar (spec do Plano 9 §4.1), e a linha do pausado.
    await page.keyboard.press('Escape')
    await expect(page.getByText('Os avisos são descartados.')).toBeVisible()
    await page.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }).click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
    const pausar = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(pausar.violations.map((v) => v.id)).toEqual([])
    await controlePositivo(page, 'confirmação de pausar')
    // O aviso de trocar o destino de um alvo com headers (B1 do Plano 10).
    await page.goto('/targets/a1/edit')
    await page.getByLabel('URL').fill('https://outro.exemplo.com/health')
    await page.getByRole('button', { name: 'Salvar' }).click()
    await expect(page.getByRole('alertdialog', { name: 'Trocar o destino do alvo?' })).toBeVisible()
    const destino = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
    expect(destino.violations.map((v) => v.id)).toEqual([])
    await controlePositivo(page, 'aviso de trocar o destino')
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
      await controlePositivo(page, passo)
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
      page.getByRole('region', { name: /Avisos/ }).getByText('Autenticador trocado. As outras sessões foram encerradas.'),
    ).toBeVisible()
    await controlePositivo(page, 'depois da troca')
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

// E7 do Plano 10: a barreira da CSP. Uma violação provocada ANTES da
// barreira, cujo aviso ainda não chegou ao processo do teste, tem de
// derrubá-la. O "expect.poll(() => violacoes(page)).toEqual([])" de antes
// passava na primeira leitura vazia, antes de o aviso chegar.
test('a barreira da CSP pega a violação que ainda não chegou', async ({ page }) => {
  await page.route('**/api/v1/auth/refresh', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
  )
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
  await page.evaluate(() => {
    const s = document.createElement('style')
    s.textContent = '.violacao-tardia{color:red}'
    document.head.append(s)
  })
  await expect(controlePositivo(page, 'violação tardia')).rejects.toThrow(/violação tardia: violações de CSP/)
})

// O outro lado da barreira: sem coletor (uma aba sem o registrarCsp), a
// violação do próprio controle nunca chega, e a barreira falha em vez de
// ler "zero violações" de uma lista que ninguém enche.
test('a barreira da CSP falha com o coletor quebrado', async ({ context }) => {
  const semColetor = await context.newPage()
  await semColetor.route('**/api/v1/auth/refresh', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
  )
  await semColetor.goto('/')
  await expect(semColetor.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
  await expect(controlePositivo(semColetor, 'sem coletor')).rejects.toThrow(/sem coletor: a violação do controle positivo/)
})

test('a tela de login, sem sessão', async ({ page }) => {
  await page.route('**/api/v1/auth/refresh', (route) =>
    route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
  )
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
  const r = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  expect(r.violations.map((v) => v.id)).toEqual([])
  await controlePositivo(page, 'login')
})

// Plano 12: o layout precisa caber no celular, inclusive campos e ações.
// Sabotagem: impor min-width: 800px ao main deve derrubar a medição.
for (const tema of ['claro', 'escuro'] as const) {
  test(`celular de 360 px: navegação e formulários sem overflow, tema ${tema}`, async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 800 })
    await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
    await imitarApi(page)
    const conferir = async (contexto: string) => {
      await expect.poll(() => page.evaluate(() => ({
        conteudo: document.documentElement.scrollWidth,
        tela: document.documentElement.clientWidth,
      })), contexto).toEqual({ conteudo: 360, tela: 360 })
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${contexto}: ${v.id}`)).toEqual([])
      await controlePositivo(page, contexto)
    }
    for (const [rota, titulo] of ROTAS) {
      await page.goto(rota)
      await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
      await esperarOConteudo(page, rota)
      await expect(page.locator('html')).toHaveClass(tema === 'escuro' ? /dark/ : /^(?!.*dark)/)
      await expect(page.getByRole('navigation', { name: 'Principal' })).toBeVisible()
      await conferir(rota)
    }
    await page.goto('/targets/new')
    await page.getByRole('button', { name: 'Adicionar header' }).click()
    await page.getByLabel('Nome do header 1', { exact: true }).fill('Authorization')
    await page.getByLabel('Valor do header 1', { exact: true }).fill('sentinela-publica-de-teste')
    await expect(page.getByRole('button', { name: 'Remover o header 1' })).toBeVisible()
    // Caber sem overflow não basta se o campo fica espremido entre outro
    // campo e a ação: uma linha de header mantém largura útil no celular.
    for (const rotulo of ['Nome do header 1', 'Valor do header 1']) {
      const caixa = await page.getByLabel(rotulo, { exact: true }).boundingBox()
      expect(caixa?.width, rotulo).toBeGreaterThanOrEqual(160)
    }
    await conferir('formulário com header')
    await page.getByRole('link', { name: 'Canais', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Canais de notificação' })).toBeFocused()
    await page.getByRole('button', { name: 'Novo canal' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await conferir('diálogo no celular')
  })
}

// P2 do shell, com a navegação no topo (PR 2 do redesign): o aviso vem
// antes do cabeçalho em toda largura. O hit-test e a geometria precisam
// enxergar o aviso inteiro, sem nada por cima.
test('aviso de HTTPS fica acima do cabeçalho em toda largura e mantém o login', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
  })
  await imitarApi(page)
  const titulo = 'Este painel precisa de HTTPS (ou de localhost).'
  for (const largura of [360, 1024, 1280, 1600]) {
    await page.setViewportSize({ width: largura, height: 900 })
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Painel', level: 1 })).toBeVisible()
    const aviso = page.getByRole('alert').filter({ hasText: titulo })
    await expect(aviso).toBeVisible()
    const texto = aviso.locator('strong')
    const caixaTexto = await texto.boundingBox()
    const cabecalho = await page.locator('header').boundingBox()
    expect(caixaTexto).not.toBeNull()
    expect(cabecalho).not.toBeNull()
    expect(caixaTexto!.y, `${largura}: aviso antes do cabeçalho`).toBeLessThan(cabecalho!.y)
    expect(await texto.evaluate((elemento) => {
      const primeiraLinha = elemento.getClientRects()[0]!
      const superior = document.elementFromPoint(primeiraLinha.left + 2, primeiraLinha.top + primeiraLinha.height / 2)
      return superior === elemento || elemento.contains(superior)
    }), `${largura}: o título não pode ficar encoberto`).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${largura}: largura`).toBe(largura)
  }
  // O mesmo componente também está no login, onde não há lateral nem
  // espaço reservado. A correção precisa ficar exclusiva ao shell.
  await page.route('**/api/v1/auth/refresh', (route) => route.fulfill({
    status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' },
  }))
  await page.setViewportSize({ width: 1024, height: 900 })
  await page.goto('/login')
  await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
  const avisoLogin = page.getByRole('alert').filter({ hasText: titulo })
  expect((await avisoLogin.boundingBox())?.x).toBe(0)
  expect((await avisoLogin.boundingBox())?.width).toBe(1024)
  await controlePositivo(page, 'aviso de HTTPS no login')
})

// Alvos de toque (WCAG 2.5.8; 44 px é a meta do 2.5.5): a área clicável de
// cada controle cobre 44 px na largura e na altura, centrados nele. A medida é pelo
// hit-test (elementFromPoint), não pela caixa: o link de texto cresce por um
// pseudo-elemento, sem mudar o layout. Sabotagem: tirar a classe
// alvo-de-toque de um link, ou o min-h-11 do controle segmentado (janela e filtro de incidentes).
async function pontosForaDoAlvo(controle: Locator): Promise<string[]> {
  return controle.evaluate((e) => {
    e.scrollIntoView({ block: 'center', inline: 'center' })
    const r = e.getBoundingClientRect()
    const cx = r.left + r.width / 2
    const cy = r.top + r.height / 2
    const meio = 21.5
    // Centro e meio de cada lado: 44 px nos dois eixos. Os cantos ficam de
    // fora porque o hit-test respeita o border-radius do botão.
    const pontos = [[0, 0], [-meio, 0], [meio, 0], [0, -meio], [0, meio]] as const
    return pontos
      .filter(([dx, dy]) => {
        const p = document.elementFromPoint(cx + dx, cy + dy)
        return !(p && (p === e || e.contains(p)))
      })
      .map(([dx, dy]) => `(${dx}, ${dy}) em ${document.elementFromPoint(cx + dx, cy + dy)?.tagName ?? 'nada'}`)
  })
}

// O pseudo-elemento que cresce um alvo não pode cobrir outro controle: com
// dois alvos colados, o toque na borda de um abre o outro. Cada controle
// visível da tela tem de receber o hit-test no centro e perto do meio de
// cada lado (2 px para dentro). Sabotagem: alargar o ::after de
// .alvo-de-toque até ele cobrir um vizinho.
async function controlesEncobertos(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const seletor = 'a[href], button, summary, input, select, textarea, [tabindex]:not([tabindex="-1"])'
    const encobertos: string[] = []
    for (const e of Array.from(document.querySelectorAll<HTMLElement>(seletor))) {
      const r = e.getBoundingClientRect()
      if (r.width === 0 || r.height === 0) continue
      e.scrollIntoView({ block: 'center', inline: 'center' })
      const c = e.getBoundingClientRect()
      const cx = c.left + c.width / 2
      const cy = c.top + c.height / 2
      const pontos = [[cx, cy], [c.left + 2, cy], [c.right - 2, cy], [cx, c.top + 2], [cx, c.bottom - 2]] as const
      for (const [x, y] of pontos) {
        const p = document.elementFromPoint(x, y)
        // Controle dentro de outro (ícone no botão, rótulo no campo) conta
        // como dele; o que importa é outro alvo por cima.
        if (p && p !== e && !e.contains(p) && !p.contains(e) && p.closest(seletor) !== null) {
          encobertos.push(`${e.tagName} "${(e.textContent ?? '').trim().slice(0, 30)}" coberto por ${p.tagName} "${(p.textContent ?? '').trim().slice(0, 30)}"`)
          break
        }
      }
    }
    return encobertos
  })
}

for (const largura of [400, 1280]) {
  test(`alvos de toque de 44 px, ${largura} px`, async ({ page }) => {
    await page.setViewportSize({ width: largura, height: 900 })
    await imitarApi(page)
    const conferir = async (alvos: [string, Locator][]) => {
      for (const [nome, controle] of alvos) {
        await expect(controle, nome).toBeVisible()
        expect(await pontosForaDoAlvo(controle), `${largura} px: ${nome}`).toEqual([])
      }
    }
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Painel', level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/')
    await conferir([
      ['nome do alvo', page.getByRole('link', { name: 'API', exact: true })],
      ['Configurar canais', page.getByRole('link', { name: 'Configurar canais' })],
      ['filtro de estado', page.getByRole('group', { name: 'Estado' }).getByRole('button', { name: /^Fora do ar/ })],
      ['filtro de tag', page.getByRole('group', { name: 'Tags' }).getByRole('button', { name: /^prod/ })],
      ['busca', page.getByRole('searchbox', { name: 'Buscar por nome ou tag' })],
      ['ordenar por', page.getByRole('combobox', { name: 'Ordenar por' })],
      // No celular a coluna Situação some, e o incidente fica a um toque no detalhe do alvo.
      ...(largura === 1280 ? ([['Ver incidente', page.getByRole('link', { name: 'Ver incidente de Banco' })]] satisfies [string, Locator][]) : []),
      ...['Painel', 'Incidentes', 'Canais', 'Status', 'Conta'].map((aba): [string, Locator] => [
        `aba ${aba}`,
        page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: new RegExp(`^${aba}`) }),
      ]),
    ])
    expect(await controlesEncobertos(page), `${largura} px: painel`).toEqual([])
    await page.goto('/?estado=down&ordem=nome')
    await esperarOConteudo(page, '/')
    await expect(page.getByRole('combobox', { name: 'Ordenar por' })).toHaveValue('nome')
    await expect(page.getByRole('group', { name: 'Estado' }).getByRole('button', { name: /^Fora do ar/ })).toHaveAttribute('aria-pressed', 'true')
    await conferir([['ordenar por, com filtro', page.getByRole('combobox', { name: 'Ordenar por' })]])
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${largura} px: rolagem lateral`).toBe(largura)
    expect(await controlesEncobertos(page), `${largura} px: painel filtrado e ordenado`).toEqual([])
    await page.goto('/targets/a1')
    await expect(page.getByRole('heading', { name: 'API', level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/targets/a1')
    await conferir([
      ['Voltar ao painel', page.getByRole('link', { name: 'Voltar ao painel' })],
      ...['1 h', '6 h', '24 h', '7 dias', '30 dias', '90 dias'].map(
        (j): [string, Locator] => [`janela ${j}`, page.getByRole('radio', { name: j, exact: true })],
      ),
      ['Todos os incidentes', page.getByRole('link', { name: 'Todos os incidentes' })],
      ['Ver dados em tabela', page.locator('summary', { hasText: 'Ver dados em tabela' })],
    ])
    await page.locator('summary', { hasText: 'Ver dados em tabela' }).click()
    expect(await controlesEncobertos(page), `${largura} px: detalhe do alvo`).toEqual([])
    await page.goto('/incidents')
    await expect(page.getByRole('heading', { name: 'Incidentes', level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/incidents')
    await conferir([
      ...['Abertos', 'Resolvidos', 'Todos'].map(
        (f): [string, Locator] => [`filtro ${f}`, page.getByRole('radio', { name: f, exact: true })],
      ),
      ['nome do alvo do incidente', page.getByRole('link', { name: 'Banco', exact: true })],
      ['Reconhecer', page.getByRole('button', { name: 'Reconhecer o incidente de Banco' })],
    ])
    expect(await controlesEncobertos(page), `${largura} px: incidentes`).toEqual([])
    // Os botões do tamanho "pequeno": o cabeçalho em toda tela, o formulário
    // de alvo, os canais e a status page. Sabotagem: tirar o min-h-11 da base
    // do Botao (components/ui/botao.tsx), ou o size-11 do botão de tema.
    await page.goto('/targets/new')
    await expect(page.getByRole('heading', { name: 'Novo alvo', level: 1 })).toBeVisible()
    await page.getByRole('button', { name: 'Adicionar header' }).click()
    await conferir([
      ['tema', page.getByRole('button', { name: /^Tema:/ })],
      ['Sair', page.getByRole('button', { name: 'Sair' })],
      ['Adicionar header', page.getByRole('button', { name: 'Adicionar header' })],
      ['Remover o header 1', page.getByRole('button', { name: 'Remover o header 1' })],
    ])
    expect(await controlesEncobertos(page), `${largura} px: novo alvo`).toEqual([])
    await page.goto('/channels')
    await expect(page.getByRole('heading', { name: 'Canais de notificação', level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/channels')
    await conferir([
      ['Reenviar verificação', page.getByRole('button', { name: /^Reenviar verificação do canal/ })],
      ['Pausar', page.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' })],
      ['Retomar', page.getByRole('button', { name: 'Retomar o canal discord.com/…/pppp' })],
      ['Apagar', page.getByRole('button', { name: 'Apagar o canal discord.com/…/abcd' })],
    ])
    expect(await controlesEncobertos(page), `${largura} px: canais`).toEqual([])
    await page.goto('/status-pages')
    await expect(page.getByRole('heading', { name: 'Status page', level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/status-pages')
    await conferir([
      ['Copiar', page.getByRole('button', { name: 'Copiar', exact: true })],
      ['Abrir', page.getByRole('link', { name: 'Abrir', exact: true })],
      ['Subir', page.getByRole('button', { name: 'Subir API' })],
      ['Descer', page.getByRole('button', { name: 'Descer API' })],
      ['Tirar', page.getByRole('button', { name: 'Tirar API da página' })],
    ])
    expect(await controlesEncobertos(page), `${largura} px: status page`).toEqual([])
    await controlePositivo(page, `alvos de toque ${largura}`)
  })
}

// Os rótulos dos eixos do gráfico cabem no <svg>: com a Inter e as cifras
// tabulares, um eixo de largura fixa cortava "2400 ms" à esquerda (virava
// "!400 ms"). Latências de 4 e 5 dígitos, nas duas resoluções da série, no
// celular e no desktop, já com a fonte carregada.
const lenta: z.input<typeof Serie> = {
  resolution: 'raw',
  items: [
    { checked_at: agora, status: 'up', latency_ms: 2400, status_code: 200, error_kind: null },
    { checked_at: agora, status: 'up', latency_ms: 1800, status_code: 200, error_kind: null },
  ],
}
const horariaLenta: z.input<typeof Serie> = {
  resolution: '1h',
  items: [
    { bucket: hora, total: 60, ok: 57, p50_ms: 4100, p95_ms: 9800, p99_ms: 9900, max_ms: 9950 },
    { bucket: agora, total: 30, ok: 30, p50_ms: 3000, p95_ms: 7000, p99_ms: 8000, max_ms: 9000 },
  ],
}

for (const largura of [400, 1280]) {
  test(`rótulos dos eixos do gráfico inteiros, ${largura} px`, async ({ page }) => {
    await page.setViewportSize({ width: largura, height: 900 })
    await imitarApi(page)
    await page.route('**/api/v1/targets/a1/checks**', (route) => {
      const janela = new URL(route.request().url()).searchParams.get('window')
      return route.fulfill({ status: 200, json: janela === '7d' ? horariaLenta : lenta })
    })
    // O maior rótulo é o teto da escala robusta: 2400 ms vai a 5000; o p95
    // de 9800 ms, a 20000.
    for (const [rota, maior] of [['/targets/a1', '5000 ms'], ['/targets/a1?janela=7d', '20000 ms']] as const) {
      await page.goto(rota)
      await expect(page.getByRole('heading', { name: 'API', level: 1 })).toBeVisible()
      const grafico = page.getByTestId('grafico-latencia')
      await expect(grafico.locator('.recharts-cartesian-axis-tick-value').first()).toBeVisible()
      await page.evaluate(() => document.fonts.ready)
      // O maior rótulo numa linha só: estreito demais, o Recharts quebra
      // "10000 ms" em "10000" e "ms" (dois <tspan>, texto "10000ms").
      await expect(grafico.locator('.recharts-cartesian-axis-tick-value').filter({ hasText: new RegExp(`^${maior}$`) })).toHaveCount(1)
      const cortados = await grafico.evaluate((figura) => {
        const svg = figura.querySelector('svg.recharts-surface')
        if (!svg) return ['sem <svg> do gráfico']
        const caixa = svg.getBoundingClientRect()
        return Array.from(figura.querySelectorAll('.recharts-cartesian-axis-tick-value'))
          .filter((t) => {
            const r = t.getBoundingClientRect()
            return r.left < caixa.left - 0.5 || r.right > caixa.right + 0.5
          })
          .map((t) => t.textContent ?? '')
      })
      expect(cortados, `${largura} px, ${rota}`).toEqual([])
    }
    await controlePositivo(page, `eixos do gráfico ${largura}`)
  })
}

// Review Focus 4 do redesign: no celular, a navegação é uma barra fixa no pé
// da tela. Rolado até o fim, o último bloco do conteúdo tem de ficar acima
// dela, ou o último botão de um formulário fica inalcançável ao toque.
// Sabotagem: tirar o pb-16 do <main> no Layout.
test('celular de 400 px: a barra inferior não cobre o fim do conteúdo', async ({ page }) => {
  await page.setViewportSize({ width: 400, height: 700 })
  await imitarApi(page)
  for (const [rota, titulo] of ROTAS) {
    await page.goto(rota)
    await expect(page.getByRole('heading', { name: titulo, level: 1 })).toBeVisible()
    await esperarOConteudo(page, rota)
    const sobra = await page.evaluate(() => {
      window.scrollTo(0, document.documentElement.scrollHeight)
      const barra = document.querySelector('nav[aria-label="Principal"]')?.getBoundingClientRect()
      const ultimo = document.querySelector('#conteudo')?.lastElementChild?.getBoundingClientRect()
      return barra && ultimo ? barra.top - ultimo.bottom : null
    })
    expect(sobra, `${rota}: barra ou conteúdo não encontrados`).not.toBeNull()
    expect(sobra ?? -1, `${rota}: o fim do conteúdo fica acima da barra`).toBeGreaterThanOrEqual(0)
  }
  await controlePositivo(page, 'barra inferior')
})

// O contador de incidentes abertos fica ao lado do texto da aba, em fluxo:
// sobreposto ao ícone, ele cobria o "I" de "Incidentes" a 1280 px.
// Sabotagem: voltar o contador a absolute sobre o ícone.
for (const largura of [400, 1280]) {
  test(`contador de abertos sem cobrir o texto da aba, ${largura} px`, async ({ page }) => {
    await page.setViewportSize({ width: largura, height: 700 })
    await imitarApi(page)
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Painel', level: 1 })).toBeVisible()
    // A API imitada tem um incidente de alvo e uma tarefa atrasada: o contador soma os dois.
    const aba = page.getByRole('navigation', { name: 'Principal' }).getByRole('link', { name: 'Incidentes, 2 abertos' })
    const texto = await aba.getByText('Incidentes', { exact: true }).boundingBox()
    const contador = await aba.locator('[aria-hidden="true"]').filter({ hasText: /^2$/ }).boundingBox()
    expect(texto, 'texto da aba').not.toBeNull()
    expect(contador, 'contador').not.toBeNull()
    const cruzaX = texto!.x < contador!.x + contador!.width && contador!.x < texto!.x + texto!.width
    const cruzaY = texto!.y < contador!.y + contador!.height && contador!.y < texto!.y + texto!.height
    expect(cruzaX && cruzaY, `${largura} px: texto ${JSON.stringify(texto)} × contador ${JSON.stringify(contador)}`).toBe(false)
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${largura} px: overflow`).toBe(largura)
    await controlePositivo(page, `contador ${largura}`)
  })
}

// Teclado com a navegação nova: o salto continua o primeiro Tab e leva ao
// conteúdo; o cabeçalho vem na ordem do DOM (as cinco abas, o tema, o Sair)
// nas duas larguras, porque é um <nav> só. No celular, cada controle do
// conteúdo que recebe o foco pelo Tab fica acima da barra fixa (WCAG
// 2.4.11). Sabotagem: tirar o scroll-padding-bottom do index.css.
const ORDEM_DO_CABECALHO = ['Pular para o conteúdo', 'Painel', 'Incidentes', 'Canais', 'Status', 'Conta', 'Tema:', 'Sair']

for (const largura of [400, 1280]) {
  test(`teclado a ${largura} px: salto, ordem do cabeçalho e foco fora da barra`, async ({ page }) => {
    await page.setViewportSize({ width: largura, height: 700 })
    await imitarApi(page)
    for (const [rota, titulo] of [['/targets/new', 'Novo alvo'], ['/settings', 'Configurações']] as const) {
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
      await esperarOConteudo(page, rota)
      const cabecalho: string[] = []
      for (let i = 0; i < ORDEM_DO_CABECALHO.length; i++) {
        await page.keyboard.press('Tab')
        cabecalho.push(await page.evaluate(() => {
          const e = document.activeElement
          return (e?.getAttribute('aria-label') ?? e?.textContent ?? '').trim()
        }))
      }
      cabecalho.forEach((nome, i) => expect(nome, `${largura} px, ${rota}: parada ${i + 1}`).toMatch(new RegExp(`^${ORDEM_DO_CABECALHO[i]}`)))

      const sobBarra: string[] = []
      let paradasNoConteudo = 0
      for (let i = 0; i < 150; i++) {
        await page.keyboard.press('Tab')
        const p = await page.evaluate(() => {
          const e = document.activeElement
          if (!e || e === document.body || !e.closest('#conteudo')) return null
          const nav = document.querySelector('nav[aria-label="Principal"]')!
          // No celular o limite é o topo da barra fixa; no desktop a
          // navegação está no topo, no fluxo, e vale caber na janela.
          const limite = getComputedStyle(nav).position === 'fixed' ? nav.getBoundingClientRect().top : window.innerHeight
          const rotulo = (e as HTMLInputElement).labels?.[0]?.textContent
          const nome = `${e.tagName.toLowerCase()} "${(e.getAttribute('aria-label') ?? rotulo ?? e.textContent ?? '').trim().slice(0, 30)}"`
          return { nome, folga: limite - e.getBoundingClientRect().bottom }
        })
        if (!p) break
        paradasNoConteudo++
        if (p.folga < 0) sobBarra.push(`${p.nome} (${p.folga.toFixed(0)} px)`)
      }
      // Sem isto, um laço que saísse no primeiro Tab passaria sem conferir nada.
      expect(paradasNoConteudo, `${largura} px, ${rota}: paradas no conteúdo`).toBeGreaterThanOrEqual(5)
      expect(sobBarra, `${largura} px, ${rota}: foco sob a barra ou fora da janela`).toEqual([])
    }
    await controlePositivo(page, `teclado ${largura}`)
  })
}

// Spec do painel denso, Estados: o filtro sem resultado, com o filtro ligado
// (fundo, borda e check), passa no axe nos dois temas e não rola de lado.
// Sabotagem: trocar o text-primaria do botão ligado por text-borda-controle.
for (const largura of [400, 1280]) {
  for (const tema of ['claro', 'escuro'] as const) {
    test(`filtro sem resultado, ${largura} px, tema ${tema}`, async ({ page }) => {
      await page.setViewportSize({ width: largura, height: 900 })
      await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
      await imitarApi(page)
      await page.route('**/api/v1/targets/summary?*', (route) => route.fulfill({ status: 200, json: { items: [] } }))
      await page.goto('/?tag=prod&estado=down&busca=nada')
      await expect(page.getByText('Nenhum monitor com esses filtros.')).toBeVisible()
      await expect(page.getByRole('group', { name: 'Tags' }).getByRole('button', { name: /^prod/ })).toHaveAttribute('aria-pressed', 'true')
      await esperarOConteudo(page, '/')
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(largura)
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => v.id)).toEqual([])
      await controlePositivo(page, `sem resultado ${largura} ${tema}`)
    })
  }
}

// Spec da tela de heartbeats: o diálogo da URL de ping tem texto longo e sem
// espaço, que quebra dentro do bloco. Sabotagem: tirar o break-all do Copiavel.
for (const largura of [400, 1280]) {
  for (const tema of ['claro', 'escuro'] as const) {
    test(`heartbeats: diálogo da URL e alvos de toque, ${largura} px, tema ${tema}`, async ({ page }) => {
      await page.setViewportSize({ width: largura, height: 900 })
      await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
      await imitarApi(page)
      await page.goto('/')
      await page.getByRole('link', { name: 'Cadastrar heartbeat' }).click()
      await page.getByLabel('Nome', { exact: true }).fill('Backup')
      await page.getByRole('button', { name: 'Cadastrar', exact: true }).click()
      const dialogo = page.getByRole('dialog', { name: 'Guarde a URL de ping' })
      await expect(dialogo).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(largura)
      const caixa = await dialogo.boundingBox()
      expect((caixa?.x ?? 0) + (caixa?.width ?? 0)).toBeLessThanOrEqual(largura)
      expect(await dialogo.evaluate((d) => d.scrollWidth <= d.clientWidth), 'nada rola de lado no diálogo').toBe(true)
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)).toEqual([])
      await controlePositivo(page, `diálogo da URL ${largura} ${tema}`)
      // O Voltar de verdade: o diálogo segura, e a URL continua na tela.
      await page.goBack()
      await expect(dialogo).toBeVisible()
      await expect(page).toHaveURL(/\/heartbeats\/new$/)
      await page.getByRole('button', { name: 'Já guardei' }).click()
      await expect(page).toHaveURL(/\/heartbeats\/h1$/)
      // A URL de ping não fica em nenhuma entrada do histórico nem no armazenamento.
      // Sabotagem: navegar com { state: { url } } depois de "Já guardei".
      for (let passo = 0; passo < 3; passo += 1) {
        const lugares = await page.evaluate(() =>
          JSON.stringify([location.href, history.state, { ...localStorage }, { ...sessionStorage }]),
        )
        expect(lugares).not.toContain(TOKEN_DE_PING)
        if (passo < 2) await page.goBack()
      }

      await page.goto('/heartbeats/h1')
      await expect(page.getByRole('heading', { name: 'Backup noturno', level: 1 })).toBeVisible()
      await expect(page.getByRole('button', { name: /^Reconhecer o incidente/ })).toBeVisible()
      await expect(page.getByText('levou 3 min')).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(largura)
      for (const controle of [
        page.getByRole('link', { name: 'Editar' }),
        page.getByRole('button', { name: 'Duplicar' }),
        page.getByRole('button', { name: 'Pausar' }),
        page.getByRole('button', { name: 'Limpar eventos' }),
        page.getByRole('button', { name: 'Apagar' }),
        page.getByRole('button', { name: /^Reconhecer o incidente/ }),
        page.getByRole('button', { name: 'Gerar nova URL' }),
      ]) {
        expect(await pontosForaDoAlvo(controle)).toEqual([])
      }

      await page.goto('/incidents')
      await expect(page.getByRole('region', { name: 'Tarefas que pedem atenção' }).getByRole('link', { name: 'Backup noturno' })).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(largura)
      const incidentes = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(incidentes.violations.map((v) => v.id)).toEqual([])
    })
  }
}

// Review Focus 3, 8 e 9 da parte 2 do plano: 32 alvos (30 no ar, um fora do
// ar com nome de 100 caracteres sem espaço, o teto do servidor, e um
// degradado). A página não rola de lado, e cada linha "no ar" fica compacta
// (até 48 px), para 30 alvos caberem em poucas telas. Sabotagens: trocar o
// h-12 da linha por h-16 (64 px); pôr whitespace-nowrap no link do nome da
// linha (a página rola de lado no celular).
const NOME_LONGO = 'ServicoSemEspacos'.repeat(6).slice(0, 100)
const TAGS_LONGAS = ['tag-de-24-caracteres-aaa', 'tag-de-24-caracteres-bbb', 'tag-de-24-caracteres-ccc']
const MUITOS_ALVOS = [
  item('a1', NOME_LONGO, 'down'),
  item('m-lento', 'Lento', 'degraded'),
  // Três tags no tamanho máximo: a célula não pode passar de uma linha.
  ...Array.from({ length: 30 }, (_, i) => ({ ...item(`m${i}`, `Serviço ${i + 1}`, 'up'), tags: TAGS_LONGAS })),
]

for (const largura of [400, 768, 1024, 1280]) {
  test(`muitos alvos e nome longo, ${largura} px`, async ({ page }) => {
    await page.setViewportSize({ width: largura, height: 900 })
    await imitarApi(page)
    // Registrada depois, esta rota vence a do imitarApi para o resumo.
    await page.route('**/api/v1/targets/summary', (route) => route.fulfill({ status: 200, json: { items: MUITOS_ALVOS } }))
    const semRolagemLateral = async (contexto: string) => {
      expect(await page.evaluate(() => document.documentElement.scrollWidth), `${contexto}: largura`).toBe(largura)
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${contexto}: ${v.id}`)).toEqual([])
      await controlePositivo(page, contexto)
    }

    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Dois serviços e uma tarefa pedem atenção.', level: 2 })).toBeVisible()
    await esperarOConteudo(page, '/')
    const monitores = page.getByRole('table', { name: /^Monitores/ })
    await expect(monitores.getByRole('rowheader')).toHaveCount(34)
    const alturas = await monitores
      .getByRole('row', { name: /Serviço \d+/ })
      .evaluateAll((linhas) => linhas.map((l) => l.getBoundingClientRect().height))
    expect(alturas).toHaveLength(30)
    expect(Math.max(...alturas), `${largura} px: a linha "no ar" mais alta`).toBeLessThanOrEqual(48)
    await expect(monitores.getByRole('link', { name: NOME_LONGO, exact: true })).toBeVisible()
    await semRolagemLateral(`painel ${largura}`)
    // O detalhe do alvo de nome longo, fora do ar.
    await page.route('**/api/v1/targets/a1', (route) =>
      route.fulfill({ status: 200, json: alvo({ id: 'a1', name: NOME_LONGO, created_at: agora }) }),
    )
    await page.goto('/targets/a1')
    await expect(page.getByRole('heading', { name: NOME_LONGO, level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/targets/a1')
    await expect(page.getByRole('region', { name: 'Fora do ar' })).toBeVisible()
    await semRolagemLateral(`detalhe ${largura}`)
    // Os incidentes, com o nome longo e sem causa (Review Focus 7).
    await page.route(
      (url) => url.pathname === '/api/v1/incidents',
      (route) =>
        route.fulfill({
          status: 200,
          json: { items: [incidente({ id: 'inc-longo', target_id: 'a1', target_name: NOME_LONGO, started_at: agora, cause: '' })] },
        }),
    )
    await page.goto('/incidents')
    await expect(page.getByRole('heading', { name: 'Incidentes', level: 1 })).toBeVisible()
    await esperarOConteudo(page, '/incidents')
    await expect(page.getByText(/^Causa não registrada · início /)).toBeVisible()
    if (largura >= 640) {
      // Na tela larga, o Reconhecer fica na linha do nome, à direita, e não
      // cai para baixo do texto. Sabotagem: tirar o sm:flex-nowrap do cartão.
      const nome = await page.getByRole('link', { name: NOME_LONGO, exact: true }).boundingBox()
      const reconhecer = await page.getByRole('button', { name: `Reconhecer o incidente de ${NOME_LONGO}` }).boundingBox()
      expect(nome && reconhecer, 'caixas do nome e do Reconhecer').toBeTruthy()
      if (nome && reconhecer) {
        expect(reconhecer.y, `${largura} px: o Reconhecer começa na linha do nome`).toBeLessThan(nome.y + nome.height)
        expect(reconhecer.x, `${largura} px: o Reconhecer fica à direita do nome`).toBeGreaterThanOrEqual(nome.x + nome.width)
      }
    }
    await semRolagemLateral(`incidentes ${largura}`)
  })
}

// Review Focus 6 da parte 3 do redesign: com erro de validação o formulário
// cresce (uma linha de erro por campo). No celular de 400 px ele continua sem
// overflow, o texto de cada erro aparece, e nem o campo que recebe o foco nem
// o botão de envio param embaixo da barra inferior fixa (WCAG 2.4.11).
// Sabotagens: tirar o scroll-padding-bottom do index.css → o botão de envio
// em foco fica sob a barra; pôr whitespace-nowrap no <p> de erro do Campo →
// overflow.
async function folgaAcimaDaBarra(page: Page): Promise<number | null> {
  return page.evaluate(() => {
    const foco = document.activeElement?.getBoundingClientRect()
    const barra = document.querySelector('nav[aria-label="Principal"]')?.getBoundingClientRect()
    return foco && barra ? barra.top - foco.bottom : null
  })
}

for (const tema of ['claro', 'escuro'] as const) {
  test(`celular de 400 px: formulários com erro de validação, tema ${tema}`, async ({ page }) => {
    await page.setViewportSize({ width: 400, height: 700 })
    await page.emulateMedia({ colorScheme: tema === 'escuro' ? 'dark' : 'light' })
    await imitarApi(page)
    const casos: { rota: string; titulo: string; enviar: string; antes?: () => Promise<void> }[] = [
      { rota: '/targets/new', titulo: 'Novo alvo', enviar: 'Cadastrar' },
      {
        rota: '/status-pages',
        titulo: 'Status page',
        enviar: 'Salvar',
        // O slug vazio dá a mensagem de erro mais longa do painel: é ela que
        // uma linha sem quebra empurraria para fora da tela.
        antes: async () => {
          await page.getByLabel('Título', { exact: true }).fill('')
          await page.getByLabel('Endereço (slug)').fill('')
          await page.getByLabel('Nome de exibição de API').fill('')
        },
      },
      { rota: '/settings', titulo: 'Configurações', enviar: 'Trocar a senha' },
    ]
    for (const c of casos) {
      await page.goto(c.rota)
      await expect(page.getByRole('heading', { name: c.titulo, level: 1 })).toBeVisible()
      await esperarOConteudo(page, c.rota)
      await c.antes?.()
      const enviar = page.getByRole('button', { name: c.enviar, exact: true })
      await enviar.click()
      const invalidos = page.locator('#conteudo [aria-invalid="true"]')
      await expect(invalidos.first(), `${c.rota}: nenhum campo inválido`).toBeVisible()
      expect(await folgaAcimaDaBarra(page), `${c.rota}: o campo com erro, em foco, fica sob a barra`).toBeGreaterThanOrEqual(0)
      for (const campo of await invalidos.all()) {
        const ids = ((await campo.getAttribute('aria-describedby')) ?? '').split(' ')
        const erro = page.locator(`[id="${ids.at(-1) ?? ''}"]`)
        await erro.scrollIntoViewIfNeeded()
        await expect(erro, `${c.rota}: o texto do erro`).toBeVisible()
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth), `${c.rota}: overflow`).toBe(400)
      // Do campo com erro até o botão de envio, pelo teclado. O Tab rola o
      // foco só até a borda da janela, que é onde a barra fica; o focus() do
      // Chromium centraliza o elemento e não provaria nada.
      await invalidos.first().focus()
      const sobBarra: string[] = []
      for (let i = 0; i < 60 && !(await enviar.evaluate((b) => b === document.activeElement)); i++) {
        await page.keyboard.press('Tab')
        const folga = await folgaAcimaDaBarra(page)
        if (folga !== null && folga < 0) {
          const nome = await page.evaluate(() => {
            const e = document.activeElement
            const rotulo = (e as HTMLInputElement | null)?.labels?.[0]?.textContent
            return `${e?.tagName.toLowerCase()} "${(e?.getAttribute('aria-label') ?? rotulo ?? e?.textContent ?? '').trim().slice(0, 30)}"`
          })
          sobBarra.push(`${nome} (${folga.toFixed(0)} px)`)
        }
      }
      await expect(enviar, `${c.rota}: o Tab chega ao botão de envio`).toBeFocused()
      expect(sobBarra, `${c.rota}: foco sob a barra, do campo com erro ao botão de envio`).toEqual([])
      const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
      expect(axe.violations.map((v) => `${c.rota}: ${v.id}`)).toEqual([])
      await controlePositivo(page, `${c.rota} com erro, ${tema}`)
    }
    // O login não tem a barra: só o overflow e o erro.
    await page.route('**/api/v1/auth/refresh', (route) =>
      route.fulfill({ status: 401, json: { error: { code: 'invalid_session', message: '' }, trace_id: 't' } }),
    )
    await page.goto('/login')
    await expect(page.getByRole('heading', { name: 'Entrar no StillOn' })).toBeVisible()
    await page.getByRole('button', { name: 'Entrar', exact: true }).click()
    await expect(page.getByText('Informe o e-mail.')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth), 'login: overflow').toBe(400)
    await controlePositivo(page, `login com erro, ${tema}`)
  })
}

// O nome de exibição vai até 100 caracteres, inclusive sem espaço. Na prévia
// da status page ele quebra em qualquer ponto, e a tela não ganha rolagem
// lateral no celular. Sabotagem: voltar o nome do cartão da prévia a <span>,
// que a regra de overflow-wrap do index.css não alcança.
test('celular de 400 px: a prévia da status page com nome de exibição longo', async ({ page }) => {
  await page.setViewportSize({ width: 400, height: 700 })
  await imitarApi(page)
  const nomeLongo = 'N'.repeat(100)
  await page.route('**/api/v1/status-pages', (route) =>
    route.fulfill({
      status: 200,
      json: { items: [pagina({ id: 'p1', items: [{ target_id: 'a1', display_name: nomeLongo }], created_at: agora })] },
    }),
  )
  await page.goto('/status-pages')
  await expect(page.getByRole('heading', { name: 'Status page', level: 1 })).toBeVisible()
  await esperarOConteudo(page, '/status-pages')
  const previa = page.getByRole('region', { name: 'Prévia aproximada' })
  await expect(previa.getByText(nomeLongo, { exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth), 'prévia: overflow').toBe(400)
  await controlePositivo(page, 'prévia com nome longo')
})
