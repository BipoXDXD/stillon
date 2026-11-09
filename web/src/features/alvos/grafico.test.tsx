import { render, screen, within } from '@testing-library/react'
import { cloneElement, isValidElement, useEffect, type ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { TooltipProps } from 'recharts'
import { GraficoDeLatencia } from './GraficoDeLatencia'

// O jsdom não passa o ponteiro sobre o gráfico: um teste abre o tooltip no
// primeiro ponto pelo defaultIndex. undefined nos demais, em que os nomes da
// legenda apareceriam em dobro.
const tooltip = vi.hoisted(() => ({ indiceAberto: undefined as number | undefined, itens: [] as unknown[] }))
const LARGURA_DO_GRAFICO = 600

// O jsdom não mede nada: o ResponsiveContainer ficaria com largura 0, e o
// Recharts não desenha o <svg>. Aqui ele passa um tamanho fixo ao gráfico e
// avisa a medida, como o de verdade.
vi.mock('recharts', async (original) => {
  const r = await original<typeof import('recharts')>()
  return {
    ...r,
    Tooltip: ({ content, ...props }: TooltipProps) => (
      <r.Tooltip
        {...props}
        defaultIndex={tooltip.indiceAberto}
        content={(p) => {
          tooltip.itens = p.payload.map((item) => ({ name: item.name, type: item.type }))
          return isValidElement<object>(content) ? cloneElement(content, p) : null
        }}
      />
    ),
    ResponsiveContainer: ({ children, onResize }: { children: ReactNode; onResize?: (largura: number, altura: number) => void }) => {
      useEffect(() => onResize?.(LARGURA_DO_GRAFICO, 300), [onResize])
      return isValidElement<{ width?: number; height?: number }>(children)
        ? cloneElement(children, { width: LARGURA_DO_GRAFICO, height: 300 })
        : null
    },
  }
})

afterEach(() => {
  tooltip.indiceAberto = undefined
})

const horas = (quantidade: number) => ({
  resolution: '1h' as const,
  items: Array.from({ length: quantidade }, (_, i) => ({
    bucket: new Date(Date.UTC(2026, 9, 1, i)).toISOString(),
    total: 60,
    ok: 60,
    p50_ms: 100,
    p95_ms: 300 + i,
    p99_ms: 400 + i,
    max_ms: 500 + i,
  })),
})

const horasSem = (quantidade: number, semP95: (i: number) => boolean, semMediana: (i: number) => boolean) => ({
  resolution: '1h' as const,
  items: horas(quantidade).items.map((h, i) => ({
    ...h,
    p50_ms: semMediana(i) ? null : h.p50_ms,
    p95_ms: semP95(i) ? null : h.p95_ms,
  })),
})

const curvasDeLinha = (c: HTMLElement) => Array.from(c.querySelectorAll('path.recharts-line-curve'))
const verticesDe = (curva: Element | undefined) => (curva?.getAttribute('d')?.match(/[ML]/g) ?? []).length
const tracosDoP95 = (c: HTMLElement) => curvasDeLinha(c).filter((curva) => curva.getAttribute('stroke-dasharray') === '4 3')
const subcaminhos = (curvas: Element[]) => curvas.reduce((n, curva) => n + (curva.getAttribute('d')?.match(/M/g) ?? []).length, 0)

describe('o p95 da faixa', () => {
  // Contorno na Area desenha as duas bordas (p50 e p95), e a de baixo suja a
  // mediana. Sabotagem: pôr stroke na Area, ou tirar a Line do p95.
  it('só o p95 é tracejado, a Area não tem contorno, e a mediana fica por cima', () => {
    const { container } = render(<GraficoDeLatencia serie={horas(4)} />)
    for (const curva of container.querySelectorAll('.recharts-area-curve')) expect(curva).toHaveAttribute('stroke', 'none')
    const [p95, mediana] = curvasDeLinha(container)
    expect(curvasDeLinha(container)).toHaveLength(2)
    expect(p95).toHaveAttribute('stroke', 'var(--borda-controle)')
    expect(p95).toHaveAttribute('stroke-width', '1.5')
    expect(p95).toHaveAttribute('stroke-dasharray', '4 3')
    expect(mediana).toHaveAttribute('stroke', 'var(--grafico-linha)')
    const area = container.querySelector('.recharts-area')
    expect(area?.compareDocumentPosition(p95 ?? container) ?? 0).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  })

  // Sabotagem: desligar a redução → os 299 vértices voltam.
  it('acima de 1 ponto a cada 6 px, o p95 é reduzido e a mediana segue inteira', () => {
    const { container } = render(<GraficoDeLatencia serie={horas(300)} />)
    const [p95, mediana] = curvasDeLinha(container)
    expect(verticesDe(mediana)).toBe(300)
    expect(verticesDe(p95)).toBeLessThan(150)
    expect(p95?.getAttribute('d')?.match(/M/g)).toHaveLength(1)
  })

  // Com o traço reduzido, os dois lados de um buraco ficavam ligados, e a faixa
  // e a mediana mostravam o buraco. Sabotagem: não abrir trecho novo no grupo
  // sem p95 → um subcaminho só.
  it('com o traço reduzido, o p95 interrompe no buraco, como a mediana', () => {
    const noBuraco = (i: number) => i >= 100 && i < 140
    const { container } = render(<GraficoDeLatencia serie={horasSem(300, noBuraco, noBuraco)} />)
    expect(subcaminhos(tracosDoP95(container))).toBe(2)
    const mediana = curvasDeLinha(container).at(-1)
    expect(subcaminhos(mediana ? [mediana] : [])).toBe(2)
  })

  // Sabotagem: interromper em qualquer hora sem p95 → vários subcaminhos.
  it('com o traço reduzido, hora isolada sem p95 não interrompe o traço', () => {
    const { container } = render(<GraficoDeLatencia serie={horasSem(300, (i) => i % 7 === 3, () => false)} />)
    expect(subcaminhos(tracosDoP95(container))).toBe(1)
  })

  // Sabotagem: reduzir sempre.
  it('com folga de largura, o p95 fica exato hora a hora', () => {
    const { container } = render(<GraficoDeLatencia serie={horas(20)} />)
    const [p95] = curvasDeLinha(container)
    expect(verticesDe(p95)).toBe(20)
  })

  // Sabotagem: voltar à bolinha cheia da legenda.
  it('a amostra da legenda mostra a faixa com a opacidade do gráfico e o traço do p95', () => {
    render(<GraficoDeLatencia serie={horas(4)} />)
    const amostra = screen.getByText('p50–p95').querySelector('svg')
    expect(amostra?.querySelector('rect')).toHaveAttribute('fill', 'var(--grafico-faixa)')
    expect(amostra?.querySelector('rect')).toHaveAttribute('fill-opacity', '0.6')
    expect(amostra?.querySelector('line')).toHaveAttribute('stroke', 'var(--borda-controle)')
    expect(amostra?.querySelector('line')).toHaveAttribute('stroke-dasharray', '4 3')
  })
})

describe('o tooltip', () => {
  const duasHoras = {
    resolution: '1h' as const,
    items: [
      { bucket: new Date(2026, 9, 14, 6).toISOString(), total: 60, ok: 60, p50_ms: 110, p95_ms: 300, p99_ms: 400, max_ms: 500 },
      { bucket: new Date(2026, 9, 14, 7).toISOString(), total: 60, ok: 60, p50_ms: 120, p95_ms: 320, p99_ms: 400, max_ms: 500 },
    ],
  }
  const aberto = (c: HTMLElement) => {
    const janela = c.querySelector<HTMLElement>('.recharts-tooltip-wrapper')
    if (janela === null) throw new Error('o tooltip não abriu')
    return within(janela)
  }

  // A cor fica só na amostra: a faixa p50–p95 sairia a 1,16:1 sobre a
  // superfície, e o padrão do Recharts não tem amostra nem unidade.
  // Sabotagem: voltar ao tooltip padrão, ou pintar o valor com a cor da série.
  it('na hora: Mediana e p95 em texto neutro, com unidade e amostra', () => {
    tooltip.indiceAberto = 0
    const { container } = render(<GraficoDeLatencia serie={duasHoras} />)
    const t = aberto(container)
    expect(t.getByText(/14\/10\/2026/)).toHaveClass('text-suave')
    expect(t.getByText('Mediana')).toHaveClass('text-suave')
    expect(t.getByText('110 ms')).toHaveClass('font-semibold')
    expect(t.getByText('110 ms').className).not.toMatch(/primaria|grafico/)
    expect(t.getByText('300 ms')).toHaveClass('font-semibold')
    expect(t.getByText('Mediana').parentElement?.querySelector('[aria-hidden="true"]')).toHaveClass('bg-primaria')
    expect(t.getByText('p95').parentElement?.querySelector('svg line')).toHaveAttribute('stroke-dasharray', '4 3')
  })

  // A Line do p95 é só desenho: quem a mostra no tooltip é o conteúdo próprio,
  // uma vez. Sabotagem: tirar o tooltipType da Line do p95.
  it('a Line do p95 fica marcada como fora do tooltip', () => {
    tooltip.indiceAberto = 0
    render(<GraficoDeLatencia serie={duasHoras} />)
    expect(tooltip.itens).toContainEqual({ name: 'p95', type: 'none' })
  })

  // O traço pode estar reduzido, e o tooltip não. Sabotagem: ler o ponto dos
  // dados desenhados → o p95 da hora aberta vira "—".
  it('com o traço reduzido, o tooltip segue com o p95 exato da hora', () => {
    tooltip.indiceAberto = 0
    const { container } = render(<GraficoDeLatencia serie={horas(300)} />)
    expect(aberto(container).getByText('300 ms')).toBeInTheDocument()
  })

  it('no check bruto: a latência, e traço no check fora do ar', () => {
    tooltip.indiceAberto = 1
    const { container } = render(
      <GraficoDeLatencia serie={{ resolution: 'raw', items: [check(0, 'up', 100), check(1, 'down', 10000), check(2, 'up', 120)] }} />,
    )
    const t = aberto(container)
    expect(t.getByText('Latência')).toHaveClass('text-suave')
    expect(t.getByText('—')).toHaveClass('font-semibold')
  })
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
