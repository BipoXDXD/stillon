import { render, screen } from '@testing-library/react'
import { cloneElement, isValidElement, type ReactNode } from 'react'
import { expect, it, vi } from 'vitest'
import { GraficoDeLatencia } from './GraficoDeLatencia'

// O jsdom não mede nada: o ResponsiveContainer ficaria com largura 0, e o
// Recharts não desenha o <svg>. Aqui ele passa um tamanho fixo ao gráfico.
vi.mock('recharts', async (original) => {
  const r = await original<typeof import('recharts')>()
  return {
    ...r,
    ResponsiveContainer: ({ children }: { children: ReactNode }) =>
      isValidElement<{ width?: number; height?: number }>(children)
        ? cloneElement(children, { width: 600, height: 300 })
        : null,
  }
})

// Revisão final: o nome e a descrição estavam no <figure>, e o que recebe o
// foco no teclado é o <svg> do Recharts (a accessibilityLayer): focado, ele
// não tinha nome nem descrição, e o <figure> e o <title> do <svg> davam o
// nome em dobro. Sabotagem: devolver aria-label e aria-describedby ao
// <figure> (e tirar do ComposedChart) → o svg focável fica sem nome.
it('o <svg> focável do gráfico tem o nome e a descrição; o <figure> não repete o nome', () => {
  render(
    <>
      <p id="resumo">Mediana 100 ms, p95 100 ms, 100,00% no ar nesta janela.</p>
      <GraficoDeLatencia
        descritoPor="resumo"
        serie={{
          resolution: 'raw',
          items: [
            { checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null },
            { checked_at: '2026-09-24T11:01:00Z', status: 'up', latency_ms: 120, status_code: 200, error_kind: null },
          ],
        }}
      />
    </>,
  )
  const svg = screen.getByRole('application', { name: 'Gráfico de latência' })
  expect(svg.tagName.toLowerCase()).toBe('svg')
  expect(svg).toHaveAttribute('tabindex', '0')
  expect(svg).toHaveAccessibleDescription(/Mediana 100 ms/)
  const figura = screen.getByTestId('grafico-latencia')
  expect(figura).not.toHaveAttribute('aria-label')
  expect(figura).not.toHaveAttribute('aria-describedby')
})
