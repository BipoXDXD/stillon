import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Minigrafico } from './Minigrafico'

// UTC e expectativas literais: nenhum oracle usa a fórmula do componente.
const agora = new Date('2026-10-05T12:37:00Z')
const atributo = (c: HTMLElement, nome: string) => Array.from(c.querySelectorAll('rect'), (r) => r.getAttribute(nome))

describe('barra das últimas 24 horas', () => {
  it('série vazia não inventa medições e diz que não há checks', () => {
    const { container } = render(<Minigrafico horas={[]} agora={agora} />, { reactStrictMode: true })
    expect(screen.getByRole('img')).toHaveAccessibleName('Últimas 24 h: sem checks')
    expect(container.querySelectorAll('rect')).toHaveLength(0)
  })

  // Sabotagem: incluir o bucket anterior/atual, ou deslocar a janela na fração de hora.
  it('inclui o primeiro e o último bucket completos e exclui as horas fora da janela', () => {
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
    )
    expect(atributo(container, 'x')).toEqual(['0', '92'])
  })

  // Sabotagem: desenhar a hora sem checks como barra.
  it('hora sem checks é lacuna, não barra', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-04T13:00:00Z', total: 0, ok: 0, p50_ms: null },
          { bucket: '2026-10-04T14:00:00Z', total: 1, ok: 1, p50_ms: 10 },
        ]}
      />,
    )
    expect(atributo(container, 'x')).toEqual(['8'])
  })

  // Forma + cor (spec do redesign, Telas > 1): a hora boa é cheia e cinza;
  // a degradada tem dois terços da altura e a fora do ar, um terço, na cor
  // do estado. Sabotagem: excluir a igualdade nos limiares, trocar as
  // alturas ou pintar a hora boa de verde.
  it('forma e cor pelo estado da hora, nos limiares inclusivos de 99,9% e 99%', () => {
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
    )
    expect(atributo(container, 'height')).toEqual(['24', '24', '16', '16', '8', '8'])
    expect(atributo(container, 'y')).toEqual(['0', '0', '8', '8', '16', '16'])
    expect(atributo(container, 'fill')).toEqual([
      'var(--borda-controle)',
      'var(--borda-controle)',
      'var(--estado-degradado)',
      'var(--estado-degradado)',
      'var(--estado-fora)',
      'var(--estado-fora)',
    ])
  })

  // A latência foi para a coluna "Latência": a altura é só do estado.
  // Sabotagem: voltar a altura proporcional ao p50.
  it('a latência não muda a altura da barra', () => {
    const { container } = render(
      <Minigrafico
        agora={agora}
        horas={[
          { bucket: '2026-10-05T07:00:00Z', total: 10, ok: 10, p50_ms: 2000 },
          { bucket: '2026-10-05T08:00:00Z', total: 10, ok: 10, p50_ms: 10 },
        ]}
      />,
    )
    expect(atributo(container, 'height')).toEqual(['24', '24'])
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
    )
    expect(screen.getByRole('img')).toHaveAccessibleName('Últimas 24 h: 2 horas no ar, 1 hora degradada, 1 hora fora do ar')
  })
})
