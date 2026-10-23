import { describe, expect, it } from 'vitest'
import { larguraDoEixo } from './eixo'

// O jsdom não mede texto: a largura do eixo sai de uma conta sobre o rótulo
// mais largo, e o e2e (a11y.spec.ts, "rótulos dos eixos do gráfico
// inteiros") confere no Chromium que nada sai do <svg>.
describe('larguraDoEixo', () => {
  it.each([
    ['', 14],
    ['0 ms', 52],
    ['2400 ms', 82],
    ['10000 ms', 92],
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
