import { render, screen } from '@testing-library/react'
import { Check } from 'lucide-react'
import { describe, expect, it } from 'vitest'
import { Selo, SeloDeEstado } from './SeloDeEstado'

// Forma + cor + texto (spec, Componentes base): pílula com o fundo 100 do
// estado, texto na cor dele e um ícone Lucide de forma própria.
describe('SeloDeEstado', () => {
  it.each([
    ['up', 'No ar', 'bg-no-ar-100', 'text-no-ar'],
    ['degraded', 'Degradado', 'bg-degradado-100', 'text-degradado'],
    ['down', 'Fora do ar', 'bg-fora-100', 'text-fora'],
    ['unknown', 'Sem dados', 'bg-sem-dados-100', 'text-sem-dados'],
    ['pausado', 'Pausado', 'bg-sem-dados-100', 'text-sem-dados'],
  ] as const)('%s: pílula "%s" com %s e %s', (estado, texto, fundo, cor) => {
    const { container } = render(<SeloDeEstado estado={estado} />)
    expect(screen.getByText(texto)).toHaveClass('rounded-full', 'text-meta', fundo, cor)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('cada estado tem um ícone de forma diferente, não só outra cor', () => {
    const { container } = render(
      <>
        {(['up', 'degraded', 'down', 'unknown', 'pausado'] as const).map((e) => (
          <SeloDeEstado key={e} estado={e} />
        ))}
      </>,
    )
    const formas = Array.from(container.querySelectorAll('svg'), (svg) => svg.getAttribute('class'))
    expect(new Set(formas).size).toBe(5)
  })
})

describe('Selo', () => {
  it('o tom primaria serve ao selo de reconhecido', () => {
    render(
      <Selo tom="primaria" Icone={Check}>
        Reconhecido
      </Selo>,
    )
    expect(screen.getByText('Reconhecido')).toHaveClass('rounded-full', 'bg-primaria-100', 'text-primaria')
  })
})
