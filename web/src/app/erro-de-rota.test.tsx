import { render, screen } from '@testing-library/react'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { expect, it } from 'vitest'
import { ErroDeRota } from './ErroDeRota'

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
