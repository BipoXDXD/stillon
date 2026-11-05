import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRef } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { BarraDeFiltros } from './BarraDeFiltros'
import { SEM_FILTROS, type Filtros } from './filtros'

function barra(busca: string, aoMudar = vi.fn<(f: Filtros) => void>()) {
  return (
    <BarraDeFiltros
      itens={[]}
      filtros={{ ...SEM_FILTROS, busca }}
      resultado={null}
      semResultado={false}
      busca={createRef()}
      aoMudar={aoMudar}
    />
  )
}

describe('campo de busca', () => {
  // D2 da revisão: a URL volta numa transição do router, atrasada em relação
  // ao teclado. O "a" que chega depois de digitar "ab" é eco do próprio campo,
  // e não pode apagar o "b". Sabotagem: tirar a guarda de pendentes.
  it('um valor antigo que volta da URL não apaga o que já foi digitado', async () => {
    const { rerender } = render(barra(''))
    const campo = screen.getByRole('searchbox', { name: 'Buscar alvo ou tag' })
    await userEvent.type(campo, 'ab')
    rerender(barra('a'))
    expect(campo).toHaveValue('ab')
    rerender(barra('ab'))
    expect(campo).toHaveValue('ab')
  })

  it('um valor que o campo não mandou veio de fora, e manda', async () => {
    const { rerender } = render(barra(''))
    const campo = screen.getByRole('searchbox', { name: 'Buscar alvo ou tag' })
    await userEvent.type(campo, 'ab')
    rerender(barra('ab'))
    rerender(barra(''))
    expect(campo).toHaveValue('')
  })

  it('digitar avisa a cada tecla', async () => {
    const aoMudar = vi.fn<(f: Filtros) => void>()
    render(barra('', aoMudar))
    await userEvent.type(screen.getByRole('searchbox', { name: 'Buscar alvo ou tag' }), 'ab')
    expect(aoMudar.mock.calls.map(([f]) => f.busca)).toEqual(['a', 'ab'])
  })
})
