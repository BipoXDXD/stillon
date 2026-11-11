import { onlineManager } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { afterEach, expect, it, vi } from 'vitest'
import { ErroDeRota } from './ErroDeRota'

afterEach(() => onlineManager.setOnline(true))

// Depois de um deploy, uma aba antiga pede o chunk velho; o handler Go
// responde 404 (e não o index), e o import() dinâmico falha com TypeError.
// A tela oferece recarregar em vez de ficar em branco.
it('chunk que não carrega vira "Há uma versão nova do painel"', async () => {
  const router = createMemoryRouter([
    {
      path: '/',
      ErrorBoundary: ErroDeRota,
      lazy: async () => {
        throw new TypeError('Failed to fetch dynamically imported module: /assets/PaginaDetalhe-velho.js')
      },
    },
  ])
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('heading', { name: 'Há uma versão nova do painel' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Recarregar' })).toBeInTheDocument()
})

it('outro erro não finge ser deploy', async () => {
  const router = createMemoryRouter([
    {
      path: '/',
      ErrorBoundary: ErroDeRota,
      Component: () => {
        throw new Error('bug')
      },
    },
  ])
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('heading', { name: 'Algo deu errado' })).toBeInTheDocument()
})

// Sem rede, recarregar leva à página de erro do navegador, e não há versão
// nova nenhuma. Sabotagem: tirar o ramo do semRede, ou trocar o revalidate
// por um botão que não faz nada.
it('chunk que não carrega sem rede diz que depende da conexão e tenta de novo sem recarregar', async () => {
  onlineManager.setOnline(false)
  const carregar = vi
    .fn<() => Promise<{ Component: () => React.JSX.Element }>>()
    .mockRejectedValueOnce(new TypeError('Failed to fetch dynamically imported module: /assets/PaginaDetalhe.js'))
    .mockResolvedValueOnce({ Component: () => <p>A tela chegou</p> })
  const router = createMemoryRouter([{ path: '/', ErrorBoundary: ErroDeRota, lazy: carregar }])
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('heading', { name: 'Esta tela depende da conexão' })).toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'Há uma versão nova do painel' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Recarregar' })).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Tentar de novo' }))
  expect(await screen.findByText('A tela chegou')).toBeInTheDocument()
  expect(carregar).toHaveBeenCalledTimes(2)
})

// A tentativa que dá certo troca a tela inteira e leva o botão junto: o foco,
// que estava nele, iria para o <body>. Vai ao título da tela que chegou.
// Sabotagem: voltar o "Tentar de novo" a um Botao comum.
it('a tentativa que dá certo leva o foco ao título da tela que chegou', async () => {
  onlineManager.setOnline(false)
  const carregar = vi
    .fn<() => Promise<{ Component: () => React.JSX.Element }>>()
    .mockRejectedValueOnce(new TypeError('Failed to fetch dynamically imported module: /assets/PaginaDetalhe.js'))
    .mockResolvedValueOnce({
      Component: () => (
        <main>
          <h1 tabIndex={-1}>A tela chegou</h1>
        </main>
      ),
    })
  const router = createMemoryRouter([{ path: '/', ErrorBoundary: ErroDeRota, lazy: carregar }])
  render(<RouterProvider router={router} />)
  await userEvent.setup().click(await screen.findByRole('button', { name: 'Tentar de novo' }))
  expect(await screen.findByRole('heading', { name: 'A tela chegou' })).toHaveFocus()
})

// Sabotagem: semRede sem o "chunk &&".
it('outro erro sem rede não vira falta de conexão', async () => {
  onlineManager.setOnline(false)
  const router = createMemoryRouter([
    {
      path: '/',
      ErrorBoundary: ErroDeRota,
      Component: () => {
        throw new Error('bug')
      },
    },
  ])
  render(<RouterProvider router={router} />)
  expect(await screen.findByRole('heading', { name: 'Algo deu errado' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Recarregar' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
})
