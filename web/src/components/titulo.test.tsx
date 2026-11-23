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
  expect(document.title).toBe('Alvo — StillOn')

  screen.getByRole('button', { name: 'Outro lugar' }).focus()
  tela.rerender(
    <StrictMode>
      <Titulo aba="Alvo renomeado">Alvo</Titulo>
      <button type="button">Outro lugar</button>
    </StrictMode>,
  )
  expect(document.title).toBe('Alvo renomeado — StillOn')
  expect(screen.getByRole('button', { name: 'Outro lugar' })).toHaveFocus()
})

// O <main> com o foco é a reserva de quem sumiu com ele (o splash, o "Tentar
// de novo") enquanto a tela ainda não tinha <h1>, como na espera da lista de
// projetos. O título que monta nele pega o foco, mesmo na primeira tela; com o
// foco no <body>, a primeira tela continua sem ele. Sabotagem: tirar o
// `|| activeElement é MAIN` do Titulo.
it('na primeira tela, o título pega o foco estacionado no <main>', () => {
  esquecerAPrimeiraTela()
  const tela = render(<main tabIndex={-1} />)
  screen.getByRole('main').focus()
  tela.rerender(
    <main tabIndex={-1}>
      <Titulo aba="Novo alvo">Novo alvo</Titulo>
    </main>,
  )
  expect(screen.getByRole('heading', { name: 'Novo alvo' })).toHaveFocus()
})

it('na primeira tela, com o foco no <body>, o título não o pega', () => {
  esquecerAPrimeiraTela()
  render(
    <main tabIndex={-1}>
      <Titulo aba="Painel">Painel</Titulo>
    </main>,
  )
  expect(screen.getByRole('heading', { name: 'Painel' })).not.toHaveFocus()
  expect(document.body).toHaveFocus()
})
