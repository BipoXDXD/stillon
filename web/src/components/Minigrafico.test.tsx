import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Minigrafico } from './Minigrafico'

// UTC e expectativas literais: nenhum oracle usa a fórmula do componente.
const agora = new Date('2026-10-05T12:37:00Z')

describe('minigráfico das últimas 24 horas', () => {
  // Sabotagem: desenhar uma barra para série vazia ou anunciar horas que não existem.
  it('série vazia não inventa medições e diz que não há checks', () => {
    const { container } = render(<Minigrafico horas={[]} agora={agora} />, { reactStrictMode: true })
    expect(screen.getByRole('img')).toHaveAccessibleName('Últimas 24 h: sem checks')
    expect(container.querySelectorAll('rect')).toHaveLength(0)
  })

  // Sabotagem: incluir o bucket anterior/atual, ou deslocar a janela na fração de hora.
  it('inclui o primeiro e último buckets completos, excluindo horas fora da janela', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-04T11:00:00Z', total: 1, ok: 1, p50_ms: 100 },
          { bucket: '2026-10-04T12:00:00Z', total: 1, ok: 1, p50_ms: 100 },
          { bucket: '2026-10-05T11:00:00Z', total: 1, ok: 1, p50_ms: 100 },
          { bucket: '2026-10-05T12:00:00Z', total: 1, ok: 1, p50_ms: 100 },
        ]}
      />,
      { reactStrictMode: true },
    )
    const barras = Array.from(container.querySelectorAll('rect'))
    expect(barras).toHaveLength(2)
    expect(barras.map((barra) => barra.getAttribute('x'))).toEqual(['0.5', '115.5'])
  })

  // Sabotagem: tratar uma hora sem checks como uma barra de falha ou zero.
  it('hora sem checks é lacuna, mantendo a medição da hora vizinha', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-04T13:00:00Z', total: 0, ok: 0, p50_ms: null },
          { bucket: '2026-10-04T14:00:00Z', total: 1, ok: 1, p50_ms: 10 },
        ]}
      />,
      { reactStrictMode: true },
    )
    const barras = Array.from(container.querySelectorAll('rect'))
    expect(barras).toHaveLength(1)
    expect(barras[0]).toHaveAttribute('x', '10.5')
    expect(barras[0]).toHaveAttribute('fill', 'var(--estado-no-ar)')
  })

  // Sabotagem: excluir igualdade nos limiares de 99,9% e 99%, ou inverter cores.
  it('cores distinguem sucesso, degradação e falha nos limiares inclusivos', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T06:00:00Z', total: 1000, ok: 1000, p50_ms: 100 },
          { bucket: '2026-10-05T07:00:00Z', total: 1000, ok: 999, p50_ms: 100 },
          { bucket: '2026-10-05T08:00:00Z', total: 1000, ok: 998, p50_ms: 100 },
          { bucket: '2026-10-05T09:00:00Z', total: 1000, ok: 990, p50_ms: 100 },
          { bucket: '2026-10-05T10:00:00Z', total: 1000, ok: 989, p50_ms: 100 },
          { bucket: '2026-10-05T11:00:00Z', total: 1000, ok: 0, p50_ms: null },
        ]}
      />,
      { reactStrictMode: true },
    )
    expect(Array.from(container.querySelectorAll('rect'), (barra) => barra.getAttribute('fill'))).toEqual([
      'var(--estado-no-ar)',
      'var(--estado-no-ar)',
      'var(--estado-degradado)',
      'var(--estado-degradado)',
      'var(--estado-fora)',
      'var(--estado-fora)',
    ])
  })

  // Sabotagem: perder escala relativa, piso visível ou fallback para p50 ausente.
  it('percentis usam escala comum, mínimo visível e altura cheia quando ausentes', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T07:00:00Z', total: 10, ok: 10, p50_ms: 200 },
          { bucket: '2026-10-05T08:00:00Z', total: 10, ok: 10, p50_ms: 100 },
          { bucket: '2026-10-05T09:00:00Z', total: 10, ok: 10, p50_ms: 0 },
          { bucket: '2026-10-05T10:00:00Z', total: 10, ok: 0, p50_ms: null },
        ]}
      />,
      { reactStrictMode: true },
    )
    const barras = Array.from(container.querySelectorAll('rect'))
    expect(barras.map((barra) => barra.getAttribute('height'))).toEqual(['28', '14', '3', '28'])
    expect(barras.map((barra) => barra.getAttribute('y'))).toEqual(['0', '14', '25', '0'])
  })

  // Sabotagem: sem piso da escala, só null produziria coordenadas NaN.
  it('horas com checks e todos os percentis ausentes continuam visíveis e finitas', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T10:00:00Z', total: 1, ok: 0, p50_ms: null },
          { bucket: '2026-10-05T11:00:00Z', total: 1, ok: 0, p50_ms: null },
        ]}
      />,
      { reactStrictMode: true },
    )
    const barras = Array.from(container.querySelectorAll('rect'))
    expect(barras.map((barra) => barra.getAttribute('height'))).toEqual(['28', '28'])
    expect(barras.map((barra) => barra.getAttribute('y'))).toEqual(['0', '0'])
  })

  // Cor nunca é o único sinal (WCAG 1.4.1): verde e vermelho têm 1,09:1 entre
  // si. Hora com falha ganha uma marca sob a barra, mais alta quando fora do
  // ar. Sabotagem: marcar a hora no ar, trocar as alturas ou perder a marca.
  it('marca sob a barra distingue degradação e falha sem depender da cor', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T07:00:00Z', total: 1000, ok: 999, p50_ms: 100 },
          { bucket: '2026-10-05T08:00:00Z', total: 1000, ok: 990, p50_ms: 100 },
          { bucket: '2026-10-05T09:00:00Z', total: 1000, ok: 989, p50_ms: 100 },
        ]}
      />,
      { reactStrictMode: true },
    )
    // Marca = retângulo de 4 px de largura sob a barra: 2 px de altura na
    // hora degradada, 4 px na hora fora do ar.
    expect(Array.from(container.querySelectorAll('path'), (m) => m.getAttribute('d'))).toEqual([
      'M100.5 32h4v2h-4z',
      'M105.5 30h4v4h-4z',
    ])
  })

  // Sabotagem: contar a hora sem checks, errar o limiar ou o plural.
  it('o nome acessível resume as horas por estado', () => {
    render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T06:00:00Z', total: 1000, ok: 1000, p50_ms: 100 },
          { bucket: '2026-10-05T07:00:00Z', total: 1000, ok: 999, p50_ms: 100 },
          { bucket: '2026-10-05T08:00:00Z', total: 1000, ok: 990, p50_ms: 100 },
          { bucket: '2026-10-05T09:00:00Z', total: 0, ok: 0, p50_ms: null },
          { bucket: '2026-10-05T10:00:00Z', total: 1000, ok: 989, p50_ms: 100 },
        ]}
      />,
      { reactStrictMode: true },
    )
    expect(screen.getByRole('img')).toHaveAccessibleName('Últimas 24 h: 2 horas no ar, 1 hora degradada, 1 hora fora do ar')
  })
})
