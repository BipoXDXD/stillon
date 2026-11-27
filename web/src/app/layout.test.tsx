import { act, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { chaves } from '@/lib/api/consultas'
import { EMAIL } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'

describe('Layout', () => {
  // As mesmas cinco abas no topo (desktop) e na barra inferior (celular):
  // um <nav> só, com nomes que não mudam com a largura da tela.
  it('a navegação principal tem as cinco abas, na ordem, e marca a atual', async () => {
    await renderizarPainel('/channels')
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    expect(within(nav).getAllByRole('link').map((a) => a.getAttribute('href'))).toEqual([
      '/projects/proj-padrao/',
      '/projects/proj-padrao/incidents',
      '/projects/proj-padrao/channels',
      '/projects/proj-padrao/status-pages',
      '/settings',
    ])
    for (const nome of ['Painel', 'Canais', 'Status', 'Conta']) {
      expect(within(nav).getByRole('link', { name: nome })).toBeInTheDocument()
    }
    expect(within(nav).getByRole('link', { name: 'Canais' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Painel' })).not.toHaveAttribute('aria-current')
  })

  // O detalhe, o cadastro e a edição de um alvo saem do Painel: a aba Painel
  // continua a atual nelas.
  it.each(['/targets/a3', '/targets/new', '/targets/a3/edit'])('em %s a aba atual é o Painel', async (rota) => {
    await renderizarPainel(rota)
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    expect(within(nav).getByRole('link', { name: 'Painel' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Painel' })).toHaveClass('bg-primaria-100')
    expect(within(nav).getAllByRole('link').filter((a) => a.hasAttribute('aria-current'))).toHaveLength(1)
  })

  it('fora do Painel, uma rota mais funda marca só a sua aba', async () => {
    await renderizarPainel('/settings/sessions')
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    expect(within(nav).getByRole('link', { name: 'Conta' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Painel' })).not.toHaveAttribute('aria-current')
  })

  it('cada aba é um alvo de toque de 44 px', async () => {
    await renderizarPainel('/')
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    for (const aba of within(nav).getAllByRole('link')) expect(aba).toHaveClass('min-h-11')
  })

  it('o item ativo usa a primária 100', async () => {
    await renderizarPainel('/incidents')
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    expect(within(nav).getByRole('link', { name: /^Incidentes/ })).toHaveClass('bg-primaria-100', 'font-semibold')
    expect(within(nav).getByRole('link', { name: 'Painel' })).not.toHaveClass('bg-primaria-100')
  })

  it('o tema é um botão de ícone, sem o select antigo', async () => {
    await renderizarPainel('/')
    expect(await screen.findByRole('button', { name: 'Tema: Sistema' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: 'Tema' })).not.toBeInTheDocument()
  })

  it('o cabeçalho tem a marca, o e-mail da conta, o tema e o Sair', async () => {
    await renderizarPainel('/')
    const cabecalho = screen.getByRole('banner')
    expect(within(cabecalho).getByText('StillOn')).toBeInTheDocument()
    expect(await within(cabecalho).findByText(EMAIL)).toBeInTheDocument()
    expect(within(cabecalho).getByRole('button', { name: /^Tema:/ })).toBeInTheDocument()
    expect(within(cabecalho).getByRole('button', { name: 'Sair' })).toBeInTheDocument()
  })

  // Sabotagem prevista: montar o aviso sem olhar o email_verified.
  it('mostra o aviso só com email_verified false', async () => {
    const { falsa, cliente } = await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    await within(screen.getByRole('banner')).findByText(EMAIL)
    const texto = 'Confirme seu e-mail para criar monitores, canais e páginas.'
    expect(screen.queryByText(texto, { exact: false })).not.toBeInTheDocument()

    falsa.estado.emailConfirmado = false
    await act(() => cliente.invalidateQueries({ queryKey: chaves.me }))
    await waitFor(() => expect(screen.getByRole('main')).toHaveTextContent(texto))
  })
})
