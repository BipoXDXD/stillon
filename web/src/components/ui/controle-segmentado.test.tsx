import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { ControleSegmentado } from './controle-segmentado'

const JANELAS = [
  { valor: '1h', rotulo: '1 h' },
  { valor: '6h', rotulo: '6 h' },
  { valor: '24h', rotulo: '24 h' },
] as const
type Janela = (typeof JANELAS)[number]['valor']

// O controle é controlado: este invólucro faz o papel da tela (a janela do
// gráfico, o filtro de incidentes), que guarda o valor.
function Tela({ inicial, aoMudar }: { inicial: Janela; aoMudar: (v: Janela) => void }) {
  const [valor, setValor] = useState<Janela>(inicial)
  return (
    <>
      <button type="button">Antes</button>
      <ControleSegmentado
        rotulo="Janela do gráfico"
        opcoes={JANELAS}
        valor={valor}
        aoMudar={(v) => {
          setValor(v)
          aoMudar(v)
        }}
      />
      <button type="button">Depois</button>
    </>
  )
}

function montar(inicial: Janela) {
  const aoMudar = vi.fn<(v: Janela) => void>()
  render(<Tela inicial={inicial} aoMudar={aoMudar} />)
  return { aoMudar, usuario: userEvent.setup() }
}

const radio = (nome: string) => screen.getByRole('radio', { name: nome })

describe('ControleSegmentado', () => {
  it('é um radiogroup com o nome dado, um radio por opção, só o valor atual marcado', () => {
    montar('6h')
    const grupo = screen.getByRole('radiogroup', { name: 'Janela do gráfico' })
    expect(within(grupo).getAllByRole('radio').map((r) => r.textContent)).toEqual(['1 h', '6 h', '24 h'])
    expect(radio('6 h')).toBeChecked()
    expect(radio('1 h')).not.toBeChecked()
    expect(radio('24 h')).not.toBeChecked()
  })

  it('o Tab entra no item marcado, e o Tab seguinte sai do grupo', async () => {
    const { usuario } = montar('6h')
    await usuario.click(screen.getByRole('button', { name: 'Antes' }))
    await usuario.tab()
    expect(radio('6 h')).toHaveFocus()
    await usuario.tab()
    expect(screen.getByRole('button', { name: 'Depois' })).toHaveFocus()
  })

  it('seta para a direita e para baixo escolhem o próximo e, no último, voltam ao primeiro', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('6 h'))
    await usuario.keyboard('{ArrowRight}')
    expect(radio('24 h')).toHaveFocus()
    expect(radio('24 h')).toBeChecked()
    await usuario.keyboard('{ArrowDown}')
    expect(radio('1 h')).toHaveFocus()
    expect(radio('1 h')).toBeChecked()
    expect(aoMudar.mock.calls).toEqual([['24h'], ['1h']])
  })

  it('seta para a esquerda e para cima escolhem o anterior e, no primeiro, vão ao último', async () => {
    const { aoMudar, usuario } = montar('1h')
    await usuario.click(radio('1 h'))
    await usuario.keyboard('{ArrowLeft}')
    expect(radio('24 h')).toHaveFocus()
    await usuario.keyboard('{ArrowUp}')
    expect(radio('6 h')).toHaveFocus()
    expect(aoMudar.mock.calls).toEqual([['24h'], ['6h']])
  })

  it('Home e End vão às pontas', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('6 h'))
    await usuario.keyboard('{End}')
    expect(radio('24 h')).toBeChecked()
    await usuario.keyboard('{Home}')
    expect(radio('1 h')).toBeChecked()
    expect(aoMudar.mock.calls).toEqual([['24h'], ['1h']])
  })

  it('clicar escolhe; clicar no já marcado não avisa de novo', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('24 h'))
    await usuario.click(radio('24 h'))
    expect(radio('24 h')).toBeChecked()
    expect(aoMudar.mock.calls).toEqual([['24h']])
  })

  it('outra tecla não mexe no valor nem no foco', async () => {
    const { aoMudar, usuario } = montar('6h')
    await usuario.click(radio('6 h'))
    await usuario.keyboard('a{Enter}')
    expect(radio('6 h')).toHaveFocus()
    expect(aoMudar).not.toHaveBeenCalled()
  })

  // Uma URL editada à mão (?janela=abc) chega como valor fora das opções.
  // Nenhum item mente que está marcado, e o grupo continua alcançável pelo
  // Tab.
  it('com valor fora das opções, nada fica marcado e o primeiro item entra na ordem do Tab', async () => {
    const aoMudar = vi.fn<(v: string) => void>()
    render(
      <>
        <button type="button">Antes</button>
        <ControleSegmentado<string> rotulo="Janela do gráfico" opcoes={JANELAS} valor="abc" aoMudar={aoMudar} />
      </>,
    )
    expect(screen.getAllByRole('radio').filter((r) => r.getAttribute('aria-checked') === 'true')).toEqual([])
    const usuario = userEvent.setup()
    await usuario.click(screen.getByRole('button', { name: 'Antes' }))
    await usuario.tab()
    expect(radio('1 h')).toHaveFocus()
  })

  it('cada item é um alvo de toque de 44 × 44 px', () => {
    montar('6h')
    for (const r of screen.getAllByRole('radio')) expect(r).toHaveClass('min-h-11', 'min-w-11')
  })
})
