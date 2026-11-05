import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EsqueletoDoPainel } from './EsqueletoDoPainel'

describe('EsqueletoDoPainel', () => {
  // P12 da auditoria: o esqueleto imita a manchete (2 blocos) e oito linhas
  // da tabela; o leitor de tela ouve só "Carregando…".
  it('imita a manchete e as linhas da tabela, e avisa que está carregando', () => {
    const { container } = render(<EsqueletoDoPainel />)
    const ocupado = container.querySelector('[aria-busy="true"]')
    expect(ocupado).toHaveTextContent('Carregando…')
    expect(ocupado?.querySelectorAll('[aria-hidden="true"]')).toHaveLength(10)
  })

  it('nenhum bloco anima com prefers-reduced-motion', () => {
    const { container } = render(<EsqueletoDoPainel />)
    const animados = Array.from(container.querySelectorAll('.animate-pulse'))
    expect(animados).toHaveLength(10)
    for (const bloco of animados) expect(bloco).toHaveClass('motion-reduce:animate-none')
  })
})
