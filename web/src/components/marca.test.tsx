import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Marca } from './Marca'

describe('Marca', () => {
  it('o nome é texto em Fraunces e o ponto índigo é decorativo', () => {
    const { container } = render(<Marca />)
    expect(screen.getByText('StillOn')).toHaveClass('font-display', 'text-xl')
    expect(container.querySelector('[aria-hidden="true"]')).toHaveClass('bg-primaria', 'rounded-full')
  })

  it('a grande, do login, tem 28 px', () => {
    render(<Marca tamanho="grande" />)
    expect(screen.getByText('StillOn')).toHaveClass('text-3xl')
  })
})
