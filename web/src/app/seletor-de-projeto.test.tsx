import { act, screen, waitFor, within } from '@testing-library/react'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
import type { ApiFalsa } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

// criarProjeto numera pelo tamanho da lista: depois do padrão, proj-2 e
// proj-3. Os nomes fora da ordem alfabética: a lista do seletor é a do
// servidor (o padrão primeiro, depois por id), e não a dos nomes.
const A = 'proj-2'
const B = 'proj-3'

function comDoisProjetos(falsa: ApiFalsa) {
  falsa.criarProjeto('Zeta')
  falsa.criarProjeto('Alfa')
}

const botaoDoSeletor = (nome: string | RegExp) => screen.findByRole('button', { name: nome })

async function abrirComEnter(r: Awaited<ReturnType<typeof renderizarPainel>>, nome: string) {
  const botao = await botaoDoSeletor(nome)
  act(() => botao.focus())
  await r.usuario.keyboard('{Enter}')
  return { botao, menu: await screen.findByRole('menu') }
}

describe('seletor de projeto', () => {
  // Sabotagem: aria-label só com o nome (sem "Projeto: ") → não acha.
  it('o botão tem o nome acessível com o projeto atual', async () => {
    await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    expect(await botaoDoSeletor('Projeto: Zeta')).toHaveAttribute('aria-haspopup', 'menu')
  })

  // Sabotagem: ordenar a lista pelo nome → Alfa vem antes de Padrão; tirar o
  // foco no atual (deixar o do Radix) → o foco cai no primeiro.
  it('abre com Enter, lista na ordem do servidor e põe o foco no atual, marcado', async () => {
    const r = await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    const { menu } = await abrirComEnter(r, 'Projeto: Zeta')
    const opcoes = within(menu).getAllByRole('menuitemradio')
    expect(opcoes.map((o) => o.textContent)).toEqual(['Padrão', 'Zeta', 'Alfa'])
    expect(opcoes.map((o) => o.getAttribute('aria-checked'))).toEqual(['false', 'true', 'false'])
    await waitFor(() => expect(within(menu).getByRole('menuitemradio', { name: 'Zeta' })).toHaveFocus())
    expect(within(menu).getByRole('menuitem', { name: 'Novo projeto' })).toBeInTheDocument()
  })

  it('as setas andam pela lista', async () => {
    const r = await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    const { menu } = await abrirComEnter(r, 'Projeto: Zeta')
    await waitFor(() => expect(within(menu).getByRole('menuitemradio', { name: 'Zeta' })).toHaveFocus())
    await r.usuario.keyboard('{ArrowDown}')
    expect(within(menu).getByRole('menuitemradio', { name: 'Alfa' })).toHaveFocus()
    await r.usuario.keyboard('{ArrowUp}{ArrowUp}')
    expect(within(menu).getByRole('menuitemradio', { name: 'Padrão' })).toHaveFocus()
  })

  it('Esc fecha e o foco volta ao botão', async () => {
    const r = await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    const { botao } = await abrirComEnter(r, 'Projeto: Zeta')
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
    await waitFor(() => expect(botao).toHaveFocus())
  })

  // O Radix segura o Tab dentro do menu. Sabotagem: tirar o onKeyDown do
  // Tab → o menu continua aberto.
  it('Tab fecha o menu', async () => {
    const r = await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    await abrirComEnter(r, 'Projeto: Zeta')
    await r.usuario.keyboard('{Tab}')
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
  })

  // Sabotagem: navegar para /projects/B/ sempre (sem caminhoNoProjeto) → a
  // seção se perde; tirar o anúncio → o anunciador fica quieto.
  it('escolher outro projeto em Incidentes fica em Incidentes, no outro projeto', async () => {
    const r = await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    const { menu } = await abrirComEnter(r, 'Projeto: Zeta')
    await r.usuario.click(within(menu).getByRole('menuitemradio', { name: 'Alfa' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${B}/incidents`))
    expect(await botaoDoSeletor('Projeto: Alfa')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('anuncios')).toHaveTextContent('Projeto Alfa.'))
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Incidentes' })).toHaveFocus())
  })

  it('escolher outro projeto no detalhe de um alvo leva ao painel do outro', async () => {
    const r = await renderizarPainel(`/projects/${A}/targets/a1`, { antes: comDoisProjetos })
    const { menu } = await abrirComEnter(r, 'Projeto: Zeta')
    await r.usuario.click(within(menu).getByRole('menuitemradio', { name: 'Alfa' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${B}/`))
  })

  // Sabotagem: navegar mesmo para o atual → o histórico ganha uma entrada.
  it('escolher o atual fecha sem navegar', async () => {
    const r = await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    const { menu } = await abrirComEnter(r, 'Projeto: Zeta')
    const chave = r.router.state.location.key
    await r.usuario.click(within(menu).getByRole('menuitemradio', { name: 'Zeta' }))
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument())
    expect(r.router.state.location.key).toBe(chave)
  })

  // Na conta, a moldura é a do projeto escolhido; trocar leva ao painel dele.
  it('em /settings mostra o escolhido e trocar leva ao painel do outro', async () => {
    const r = await renderizarPainel('/settings', { antes: comDoisProjetos })
    const botao = await botaoDoSeletor('Projeto: Padrão')
    await r.usuario.click(botao)
    await r.usuario.click(within(await screen.findByRole('menu')).getByRole('menuitemradio', { name: 'Alfa' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${B}/`))
  })

  // O nome acessível é o próprio texto visível (WCAG 2.5.3), decisão do dono
  // de 2026-11-23. Sabotagem: mostrar o padrão quando o da URL não é da
  // pessoa → o botão diz "Projeto: Padrão"; voltar o aria-label "Projeto:
  // nenhum escolhido" → o nome não é o texto visível.
  it('num projeto fora da lista, "Escolher projeto", sem item marcado', async () => {
    const r = await renderizarPainel(`/projects/${crypto.randomUUID()}/`, { antes: comDoisProjetos })
    await screen.findByRole('heading', { level: 1, name: 'Página não encontrada' })
    const botao = await botaoDoSeletor('Escolher projeto')
    expect(botao).toHaveTextContent('Escolher projeto')
    expect(botao).not.toHaveAttribute('aria-label')
    await r.usuario.click(botao)
    const menu = await screen.findByRole('menu')
    expect(within(menu).getAllByRole('menuitemradio').map((o) => o.getAttribute('aria-checked'))).toEqual([
      'false',
      'false',
      'false',
    ])
  })

  // Sabotagem: mostrar o texto na hora → aparece antes dos 300 ms; tirar o
  // aria-disabled → o menu abre sem lista; tirar o aria-label da carga → o
  // botão fica sem nome nos primeiros 300 ms.
  it('com a lista carregando, "Carregando…" depois de 300 ms, e o menu não abre', async () => {
    let soltar = () => {}
    const presa = new Promise<void>((resolve) => {
      soltar = resolve
    })
    const r = await renderizarPainel('/settings', {
      esperarARaiz: false,
      antes: () => servidor.use(http.get('*/api/v1/projects', async () => { await presa })),
    })
    // No cabeçalho: a tela da conta tem o seu próprio "Carregando…".
    const cabecalho = await screen.findByRole('banner')
    expect(within(cabecalho).queryByText('Carregando…')).not.toBeInTheDocument()
    expect(within(cabecalho).getByRole('button', { name: 'Projeto: carregando' })).toHaveAttribute('aria-disabled', 'true')
    const texto = await within(cabecalho).findByText('Carregando…', {}, { timeout: 2000 })
    const botao = texto.closest('button')
    expect(botao).toHaveAttribute('aria-disabled', 'true')
    if (botao) await r.usuario.click(botao)
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    soltar()
    expect(await botaoDoSeletor('Projeto: Padrão')).toBeInTheDocument()
  })

  // Sabotagem: tirar o "Tentar de novo" → a lista não volta sem recarregar.
  it('com a lista em erro, "Projetos indisponíveis"; o menu diz o porquê e tenta de novo', async () => {
    const r = await renderizarPainel('/settings', {
      antes: (falsa) => falsa.estado.forcar.set('GET /projects', { status: 503, code: 'unavailable' }),
    })
    const botao = await botaoDoSeletor('Projetos indisponíveis')
    await r.usuario.click(botao)
    const menu = await screen.findByRole('menu')
    expect(menu).toHaveTextContent('Serviço indisponível no momento.')
    r.falsa.estado.forcar.delete('GET /projects')
    await r.usuario.click(within(menu).getByRole('menuitem', { name: 'Tentar de novo' }))
    expect(await botaoDoSeletor('Projeto: Padrão')).toBeInTheDocument()
  })
})

describe('título da aba com o projeto', () => {
  // Sabotagem: Titulo sem o projeto → "Incidentes — StillOn".
  it('tela de projeto: "<tela> — <projeto> — StillOn"', async () => {
    await renderizarPainel(`/projects/${A}/incidents`, { antes: comDoisProjetos })
    await screen.findByRole('heading', { level: 1, name: 'Incidentes' })
    await waitFor(() => expect(document.title).toBe('Incidentes — Zeta — StillOn'))
  })

  // Sabotagem: a NaoEncontrada sem o semProjeto → leva o nome do projeto.
  it('a página não encontrada fica sem o projeto', async () => {
    await renderizarPainel(`/projects/${A}/nao-existe`, { antes: comDoisProjetos })
    await screen.findByRole('heading', { level: 1, name: 'Página não encontrada' })
    await waitFor(() => expect(document.title).toBe('Página não encontrada — StillOn'))
  })

  it('a conta fica sem o projeto', async () => {
    await renderizarPainel('/settings', { antes: comDoisProjetos })
    await screen.findByRole('heading', { level: 1, name: 'Configurações' })
    await waitFor(() => expect(document.title).toBe('Configurações — StillOn'))
  })
})
