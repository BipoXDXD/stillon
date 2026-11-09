import { render, screen } from '@testing-library/react'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { beforeAll, expect, it } from 'vitest'
import { rotas } from './rotas'

const PRAZO_DOS_IMPORTS_MS = 30_000

// O teste é síncrono e não pode terminar com um import() em andamento: o
// módulo já em cache resolve no turno seguinte, depois da asserção, e o splash
// continua na tela quando ela roda.
beforeAll(async () => {
  await Promise.all([import('@/features/alvos/PaginaDetalhe'), import('@/features/configuracoes/PaginaConfiguracoes')])
}, PRAZO_DOS_IMPORTS_MS)

// Na carga direta de uma rota sob demanda, o router só renderiza as rotas
// depois de o chunk chegar. Sem HydrateFallback, a tela fica em branco nesse
// intervalo. Sabotagem: tirar o HydrateFallback da rota do ExigirSessao.
it.each(['/settings', '/targets/alvo-1'])('%s mostra o splash enquanto o chunk da rota não chega', (rota) => {
  const router = createMemoryRouter(rotas, { initialEntries: [rota] })
  render(<RouterProvider router={router} />)
  expect(screen.getByText('Carregando o StillOn…')).toBeInTheDocument()
  router.dispose()
})
