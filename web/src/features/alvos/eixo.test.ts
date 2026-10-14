import { describe, expect, it } from 'vitest'
import { larguraDoEixo, maiorLatencia, rotuloDoTopo } from './eixo'

// O jsdom não mede texto: a largura do eixo sai de uma conta sobre o rótulo
// mais largo, e o e2e (a11y.spec.ts, "rótulos dos eixos do gráfico
// inteiros") confere no Chromium que nada sai do <svg>.
describe('larguraDoEixo', () => {
  it.each([
    ['', 14],
    ['0 ms', 52],
    ['2400 ms', 82],
    ['10000 ms', 92],
    ['100%', 61],
  ])('"%s" pede %i px', (rotulo, largura) => {
    expect(larguraDoEixo(rotulo)).toBe(largura)
  })

  // A largura fixa de antes (72 px) cortava "2400 ms" à esquerda.
  it('"2400 ms" pede mais que os 72 px fixos de antes', () => {
    expect(larguraDoEixo('2400 ms')).toBeGreaterThan(72)
  })

  it('cada cifra a mais alarga o eixo', () => {
    expect(larguraDoEixo('10000 ms')).toBeGreaterThan(larguraDoEixo('9000 ms'))
  })
})

describe('rotuloDoTopo', () => {
  // O topo da escala é o primeiro valor redondo (1, 2 ou 5 vezes uma
  // potência de 10) que cobre o maior ponto: o Recharts arredonda o último
  // tique para cima, e o rótulo pode ganhar uma cifra (9800 vira 10000).
  it.each([
    [0, '0 ms'],
    [1, '1 ms'],
    [120, '200 ms'],
    [200, '200 ms'],
    [201, '500 ms'],
    [2400, '5000 ms'],
    [5001, '10000 ms'],
    [9800, '10000 ms'],
    [10000, '10000 ms'],
  ])('maior latência %i → "%s"', (maior, rotulo) => {
    expect(rotuloDoTopo(maior)).toBe(rotulo)
  })
})

describe('maiorLatencia', () => {
  it('sem pontos, 0', () => {
    expect(maiorLatencia([])).toBe(0)
  })

  it('na série bruta, a maior latência; o ponto fora do ar (latência nula) não conta', () => {
    expect(
      maiorLatencia([
        { t: 1, latencia: 120, fora: null },
        { t: 2, latencia: null, fora: 0 },
        { t: 3, latencia: 2400, fora: null },
      ]),
    ).toBe(2400)
  })

  it('na série por hora, o topo da faixa p50–p95', () => {
    expect(
      maiorLatencia([
        { t: 1, p50: 4100, faixa: [4100, 9800], uptime: 0.95 },
        { t: 2, p50: null, faixa: null, uptime: null },
        { t: 3, p50: 3000, faixa: [3000, 7000], uptime: 1 },
      ]),
    ).toBe(9800)
  })
})
