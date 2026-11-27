import { render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { beforeAll, describe, expect, it } from 'vitest'
import { PROJETO_PADRAO } from '@/test/api-falsa'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
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
it.each(['/settings', `/projects/${PROJETO_PADRAO}/targets/alvo-1`])('%s mostra o splash enquanto o chunk da rota não chega', (rota) => {
  const router = createMemoryRouter(rotas, { initialEntries: [rota] })
  render(<RouterProvider router={router} />)
  expect(screen.getByText('Carregando o StillOn…')).toBeInTheDocument()
  router.dispose()
})

// Os links de antes dos projetos (favoritos, e-mails de aviso) continuam
// valendo: levam ao mesmo lugar no Projeto padrão.
describe('links antigos', () => {
  it('/targets/a1 vai ao detalhe do alvo no projeto padrão', async () => {
    const r = await renderizarPainel('/targets/a1', { antes: (f) => (f.estado.alvos = [alvo({ id: 'a1' })]) })
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${PROJETO_PADRAO}/targets/a1`))
    expect(await screen.findByRole('heading', { level: 1, name: 'API' })).toBeInTheDocument()
  })

  it('/incidents?status=open preserva a search', async () => {
    const r = await renderizarPainel('/incidents?status=open')
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${PROJETO_PADRAO}/incidents`))
    expect(r.router.state.location.search).toBe('?status=open')
  })

  it('o redirecionamento troca a entrada do histórico, sem criar outra', async () => {
    const antes = window.history.length
    const r = await renderizarPainel('/channels', { navegador: true })
    await waitFor(() => expect(window.location.pathname).toBe(`/projects/${PROJETO_PADRAO}/channels`))
    expect(r.router.state.historyAction).toBe('REPLACE')
    expect(window.history.length).toBe(antes)
  })
})
