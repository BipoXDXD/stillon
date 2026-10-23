import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { duracaoDoIncidente, periodoDoIncidente, SeloDoIncidente } from './SeloDoIncidente'

describe('SeloDoIncidente', () => {
  it.each([
    [{ resolved_at: null, acknowledged_at: null }, 'Em andamento', 'bg-fora-100'],
    [{ resolved_at: null, acknowledged_at: '2026-09-24T11:10:00Z' }, 'Reconhecido', 'bg-primaria-100'],
    [{ resolved_at: '2026-09-24T11:30:00Z', acknowledged_at: '2026-09-24T11:10:00Z' }, 'Resolvido', 'bg-sem-dados-100'],
  ])('%j → "%s"', (i, texto, fundo) => {
    const { container } = render(<SeloDoIncidente incidente={i} />)
    expect(screen.getByText(texto)).toHaveClass('rounded-full', fundo)
    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})

// R1.4 do backlog: a duração de um incidente, escrita uma vez para o
// detalhe do alvo e a tela de incidentes.
describe('duracaoDoIncidente', () => {
  const agora = new Date('2026-09-24T12:00:00Z')

  it('aberto: há quanto tempo, até agora', () => {
    expect(duracaoDoIncidente({ started_at: '2026-09-24T11:00:00Z', resolved_at: null }, agora)).toBe('há 1 h 0 min')
  })

  it('resolvido: quanto durou, sem olhar o agora', () => {
    expect(duracaoDoIncidente({ started_at: '2026-09-24T11:00:00Z', resolved_at: '2026-09-24T11:30:00Z' }, agora)).toBe(
      'durou 30 min',
    )
  })
})

describe('periodoDoIncidente', () => {
  const agora = new Date('2026-09-24T12:00:00Z')

  it('aberto: o início e há quanto tempo', () => {
    expect(periodoDoIncidente({ started_at: '2026-09-24T11:00:00Z', resolved_at: null }, agora)).toMatch(/^início .+ · há 1 h 0 min$/)
  })

  it('resolvido: o início e quanto durou', () => {
    expect(periodoDoIncidente({ started_at: '2026-09-24T11:00:00Z', resolved_at: '2026-09-24T11:30:00Z' }, agora)).toMatch(
      /^início .+ · durou 30 min$/,
    )
  })
})
