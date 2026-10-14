import { describe, expect, it } from 'vitest'
import { dominioX, escalaRobusta, textoDoPico, ticksDoEixoX } from './escala'

const ponto = (ms: number, t = 0) => ({ t, ms })
const vezes = (n: number, ms: number) => Array.from({ length: n }, (_, i) => ponto(ms, i))
// Datas pelo construtor local: os testes valem em qualquer fuso.
const local = (mes: number, dia: number, h: number, min = 0) => new Date(2026, mes - 1, dia, h, min).getTime()

describe('escalaRobusta', () => {
  it('sem pontos, o teto mínimo', () => {
    expect(escalaRobusta([])).toEqual({ teto: 10, pico: null, foraDaEscala: 0 })
  })

  // Review Focus 6.
  it('um ponto só: o próximo número redondo acima dele com 10% de ar', () => {
    expect(escalaRobusta([ponto(120)])).toEqual({ teto: 200, pico: null, foraDaEscala: 0 })
  })

  it('tudo perto do p95: a escala cobre o maior ponto', () => {
    const pontos = Array.from({ length: 20 }, (_, i) => ponto((i + 1) * 100, i))
    expect(escalaRobusta(pontos)).toEqual({ teto: 2500, pico: null, foraDaEscala: 0 })
  })

  // A spec: o teto vem do p95 com folga, e o pico acima dele é anotado em
  // vez de esmagar a série.
  it('um pico isolado fica fora da escala e é o pico anotado', () => {
    const pico = ponto(2400, 99)
    expect(escalaRobusta([...vezes(20, 100), pico])).toEqual({ teto: 200, pico, foraDaEscala: 1 })
  })

  it('vários pontos fora da escala: o pico é o maior, e a conta é de todos', () => {
    expect(escalaRobusta([...vezes(40, 100), ponto(1800, 98), ponto(2400, 99)])).toEqual({
      teto: 200,
      pico: ponto(2400, 99),
      foraDaEscala: 2,
    })
  })

  // Fronteira no teto: 200 cabe; 201 fica de fora.
  it('ponto igual ao teto cabe; um acima dele, não', () => {
    expect(escalaRobusta([...vezes(20, 100), ponto(200, 99)]).pico).toBeNull()
    expect(escalaRobusta([...vezes(20, 100), ponto(201, 99)]).pico).toEqual(ponto(201, 99))
  })

  it('latência zero não dá teto zero', () => {
    expect(escalaRobusta([ponto(0), ponto(0, 1)]).teto).toBe(10)
  })
})

describe('textoDoPico', () => {
  const pico = { t: local(10, 14, 3, 10), ms: 2400 }

  it('sem pico, nada', () => {
    expect(textoDoPico({ teto: 200, pico: null, foraDaEscala: 0 }, false)).toBeNull()
  })

  it('um ponto fora: o pico e a hora', () => {
    expect(textoDoPico({ teto: 200, pico, foraDaEscala: 1 }, false)).toBe('pico de 2.400 ms às 03:10, fora da escala')
  })

  it('janela de dias: o pico com a data', () => {
    expect(textoDoPico({ teto: 200, pico, foraDaEscala: 1 }, true)).toBe('pico de 2.400 ms em 14/10, 03:10, fora da escala')
  })

  it('vários pontos fora: quantos, e o pico', () => {
    expect(textoDoPico({ teto: 200, pico, foraDaEscala: 3 }, false)).toBe('3 pontos fora da escala; o pico, 2.400 ms às 03:10')
  })
})

describe('dominioX', () => {
  it('sem pontos, sem domínio (a tela mostra uma frase no lugar do gráfico)', () => {
    expect(dominioX([], [])).toBeNull()
  })

  it('do primeiro ao último ponto, esticado até o fim da última faixa', () => {
    expect(dominioX([0, 7_200_000], [{ de: 3_600_000, ate: 9_000_000 }])).toEqual([0, 9_000_000])
  })

  // Review Focus 6: um ponto só ganha 1 h de abertura, centrada nele.
  it('abertura mínima de 1 h, centrada', () => {
    expect(dominioX([10_000_000], [])).toEqual([10_000_000 - 1_800_000, 10_000_000 + 1_800_000])
  })
})

describe('ticksDoEixoX', () => {
  it('1 h: quartos de hora', () => {
    expect(ticksDoEixoX(local(10, 14, 9, 7), local(10, 14, 10, 7))).toEqual({
      marcas: [local(10, 14, 9, 15), local(10, 14, 9, 30), local(10, 14, 9, 45), local(10, 14, 10, 0)],
      porDia: false,
    })
  })

  it('a marca na ponta do domínio entra', () => {
    expect(ticksDoEixoX(local(10, 14, 9, 0), local(10, 14, 10, 0)).marcas).toEqual([
      local(10, 14, 9, 0),
      local(10, 14, 9, 15),
      local(10, 14, 9, 30),
      local(10, 14, 9, 45),
      local(10, 14, 10, 0),
    ])
  })

  it('6 h: horas cheias', () => {
    expect(ticksDoEixoX(local(10, 14, 3, 20), local(10, 14, 9, 20)).marcas).toEqual([4, 5, 6, 7, 8, 9].map((h) => local(10, 14, h)))
  })

  it('24 h: de seis em seis horas, contadas da meia-noite', () => {
    expect(ticksDoEixoX(local(10, 13, 9, 7), local(10, 14, 9, 7))).toEqual({
      marcas: [local(10, 13, 12), local(10, 13, 18), local(10, 14, 0), local(10, 14, 6)],
      porDia: false,
    })
  })

  it('7 dias: meias-noites, de dois em dois dias', () => {
    expect(ticksDoEixoX(local(10, 7, 9), local(10, 14, 9))).toEqual({
      marcas: [local(10, 9, 0), local(10, 11, 0), local(10, 13, 0)],
      porDia: true,
    })
  })

  it('90 dias: no máximo sete marcas', () => {
    const { marcas, porDia } = ticksDoEixoX(local(7, 16, 9), local(10, 14, 9))
    expect(porDia).toBe(true)
    expect(marcas.length).toBeGreaterThan(0)
    expect(marcas.length).toBeLessThanOrEqual(7)
  })
})
