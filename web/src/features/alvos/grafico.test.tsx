import { render, screen } from '@testing-library/react'
import { cloneElement, isValidElement, type ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
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

// Datas pelo construtor local: as horas da legenda valem em qualquer fuso.
const check = (minuto: number, status: 'up' | 'down', latency_ms: number) => ({
  checked_at: new Date(2026, 9, 14, 3, minuto).toISOString(),
  status,
  latency_ms,
  status_code: status === 'down' ? null : 200,
  error_kind: status === 'down' ? 'timeout' : null,
})

describe('o gráfico do redesign', () => {
  // A spec: o pico acima do teto é anotado na legenda em vez de esmagar a
  // série, e o leitor de tela o ouve junto do resumo. Sabotagem: tirar o id
  // do pico do aria-describedby.
  it('o pico fora da escala vai para a legenda e para a descrição do <svg>', () => {
    render(
      <>
        <p id="resumo">Mediana 100 ms.</p>
        <GraficoDeLatencia
          descritoPor="resumo"
          serie={{ resolution: 'raw', items: [...Array.from({ length: 20 }, (_, i) => check(i, 'up', 100)), check(20, 'up', 2400)] }}
        />
      </>,
    )
    expect(screen.getByText('pico de 2.400 ms às 03:20, fora da escala')).toBeInTheDocument()
    expect(screen.getByRole('application', { name: 'Gráfico de latência' })).toHaveAccessibleDescription(
      'Mediana 100 ms. pico de 2.400 ms às 03:20, fora da escala',
    )
  })

  it('sem nada fora da escala, a legenda não fala de pico', () => {
    render(<GraficoDeLatencia serie={{ resolution: 'raw', items: [check(0, 'up', 100), check(1, 'up', 120)] }} />)
    expect(screen.queryByText(/pico/)).not.toBeInTheDocument()
  })

  // Forma + cor: a queda é uma faixa hachurada com o padrão do próprio
  // gráfico. Sabotagem: pintar a faixa de var(--estado-fora) liso.
  it('a queda vira faixa hachurada, e a legenda diz "Fora do ar"', () => {
    const { container } = render(
      <GraficoDeLatencia serie={{ resolution: 'raw', items: [check(0, 'up', 100), check(1, 'down', 10000), check(2, 'up', 100)] }} />,
    )
    const areas = container.querySelectorAll('.recharts-reference-area-rect')
    expect(areas).toHaveLength(1)
    const id = areas[0]?.getAttribute('fill')?.match(/^url\(#(.+)\)$/)?.[1]
    expect(id).toMatch(/^hachura-/)
    expect(container.querySelector(`pattern[id="${id}"] path`)).toHaveAttribute('stroke', 'var(--estado-fora)')
    expect(screen.getByText('Fora do ar')).toBeInTheDocument()
  })

  it('horária: legenda de mediana e faixa p50–p95, e a hora com falhas hachurada', () => {
    const { container } = render(
      <GraficoDeLatencia
        serie={{
          resolution: '1h',
          items: [
            { bucket: '2026-10-14T06:00:00Z', total: 60, ok: 60, p50_ms: 110, p95_ms: 300, p99_ms: 400, max_ms: 500 },
            { bucket: '2026-10-14T07:00:00Z', total: 60, ok: 57, p50_ms: 120, p95_ms: 320, p99_ms: 400, max_ms: 500 },
          ],
        }}
      />,
    )
    expect(screen.getByText('Mediana')).toBeInTheDocument()
    expect(screen.getByText('p50–p95')).toBeInTheDocument()
    expect(screen.getByText('Horas com menos de 99% no ar')).toBeInTheDocument()
    expect(container.querySelectorAll('.recharts-reference-area-rect')).toHaveLength(1)
  })

  // Review Focus 6. Caracterização: passa de primeira; a fronteira (a
  // abertura mínima do domínio) é testada em escala.test.ts.
  it('uma série de um ponto só desenha sem coordenadas inválidas', () => {
    const { container } = render(<GraficoDeLatencia serie={{ resolution: 'raw', items: [check(0, 'up', 120)] }} />)
    expect(screen.getByRole('application', { name: 'Gráfico de latência' })).toBeInTheDocument()
    expect(container.innerHTML).not.toMatch(/NaN|Infinity/)
  })

  it('série vazia não desenha nada', () => {
    const { container } = render(<GraficoDeLatencia serie={{ resolution: 'raw', items: [] }} />)
    expect(container).toBeEmptyDOMElement()
  })
})
