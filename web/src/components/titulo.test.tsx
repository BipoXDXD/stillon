import { render, screen } from '@testing-library/react'
import { StrictMode } from 'react'
import { expect, it } from 'vitest'
import { esquecerAPrimeiraTela, Titulo } from './Titulo'

// O foco é da montagem (D20); o título da aba acompanha `aba`. Uma `aba` que
// muda depois, na mesma tela, atualiza a aba sem roubar o foco de onde o
// usuário estiver. Sabotagem prevista: pôr [aba] nas dependências do efeito
// do foco.
it('uma aba que muda depois da montagem não rouba o foco', () => {
  esquecerAPrimeiraTela()
  // A primeira tela da carga não recebe o foco; esta a consome.
  render(<Titulo aba="Primeira">Primeira</Titulo>).unmount()

  const tela = render(
    <StrictMode>
      <Titulo aba="Alvo">Alvo</Titulo>
      <button type="button">Outro lugar</button>
    </StrictMode>,
  )
  // Uma tela depois de navegar: o título recebe o foco ao montar.
  expect(screen.getByRole('heading', { name: 'Alvo' })).toHaveFocus()
  expect(document.title).toBe('Alvo — Vigilume')

  screen.getByRole('button', { name: 'Outro lugar' }).focus()
  tela.rerender(
    <StrictMode>
      <Titulo aba="Alvo renomeado">Alvo</Titulo>
      <button type="button">Outro lugar</button>
    </StrictMode>,
  )
  expect(document.title).toBe('Alvo renomeado — Vigilume')
  expect(screen.getByRole('button', { name: 'Outro lugar' })).toHaveFocus()
})
