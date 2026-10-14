import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRef } from 'react'
import { describe, expect, it } from 'vitest'
import { CaixaDeMarcar, Campo, Entrada, ErroDoFormulario, Selecao } from './campo'

describe('Campo', () => {
  it('liga o rótulo, a ajuda e o erro ao controle', () => {
    render(
      <Campo rotulo="Nome" ajuda="Como aparece no painel." erro="Obrigatório.">
        {(a) => <Entrada {...a} />}
      </Campo>,
    )
    const entrada = screen.getByRole('textbox', { name: 'Nome' })
    expect(entrada).toHaveAccessibleDescription('Como aparece no painel. Obrigatório.')
    expect(entrada).toBeInvalid()
  })

  it('sem erro, o controle é válido e só a ajuda o descreve', () => {
    render(
      <Campo rotulo="Nome" ajuda="Como aparece no painel.">
        {(a) => <Entrada {...a} />}
      </Campo>,
    )
    const entrada = screen.getByRole('textbox', { name: 'Nome' })
    expect(entrada).toHaveAccessibleDescription('Como aparece no painel.')
    expect(entrada).not.toBeInvalid()
  })

  // 44 px (alvo de toque) e 16 px de fonte no celular: abaixo disso o
  // Safari do iPhone amplia a página ao focar o campo.
  it('a entrada e a seleção têm 44 px e 16 px de fonte no celular', () => {
    render(
      <>
        <Entrada aria-label="URL" />
        <Selecao aria-label="Método">
          <option>GET</option>
        </Selecao>
      </>,
    )
    for (const controle of [screen.getByRole('textbox', { name: 'URL' }), screen.getByRole('combobox', { name: 'Método' })]) {
      expect(controle).toHaveClass('min-h-11', 'text-base', 'border-borda-controle')
    }
  })
})

describe('CaixaDeMarcar', () => {
  it('é um checkbox com o próprio rótulo, que alterna ao clicar no texto', async () => {
    render(<CaixaDeMarcar>Ativo (sondar este alvo)</CaixaDeMarcar>)
    const caixa = screen.getByRole('checkbox', { name: 'Ativo (sondar este alvo)' })
    await userEvent.click(screen.getByText('Ativo (sondar este alvo)'))
    expect(caixa).toBeChecked()
  })

  it('repassa ref e atributos ao input, como o register do react-hook-form pede', () => {
    const ref = createRef<HTMLInputElement>()
    render(
      <CaixaDeMarcar ref={ref} name="enabled" defaultChecked>
        Ativo
      </CaixaDeMarcar>,
    )
    const caixa = screen.getByRole('checkbox', { name: 'Ativo' })
    expect(ref.current).toBe(caixa)
    expect(caixa).toHaveAttribute('name', 'enabled')
    expect(caixa).toBeChecked()
  })

  it('a linha inteira é a área de toque, com 44 px', () => {
    render(<CaixaDeMarcar>Guardei os códigos</CaixaDeMarcar>)
    expect(screen.getByRole('checkbox', { name: 'Guardei os códigos' }).closest('label')).toHaveClass('min-h-11')
  })
})

describe('ErroDoFormulario', () => {
  it('anuncia o erro do formulário e some sem texto', () => {
    const { rerender } = render(<ErroDoFormulario>Senha ou código incorretos.</ErroDoFormulario>)
    expect(screen.getByRole('alert')).toHaveTextContent('Senha ou código incorretos.')
    rerender(<ErroDoFormulario />)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
