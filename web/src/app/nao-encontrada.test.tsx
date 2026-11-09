import { render, screen } from '@testing-library/react'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { expect, it } from 'vitest'
import { NaoEncontrada } from './NaoEncontrada'

// O link é a única saída da tela e tinha só a altura da linha de texto. A
// medida de 44 px é do e2e/a11y.spec.ts, que não visita esta rota; aqui fica a
// classe que cresce a área de toque. Sabotagem: tirar alvo-de-toque do link.
it('o link de volta ao painel tem a área de toque de 44 px', () => {
  const router = createMemoryRouter([{ path: '/', Component: NaoEncontrada }])
  render(<RouterProvider router={router} />)
  expect(screen.getByRole('link', { name: 'Voltar ao painel' })).toHaveClass('alvo-de-toque')
  router.dispose()
})
