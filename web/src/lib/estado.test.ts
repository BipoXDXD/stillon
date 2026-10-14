import { describe, expect, it } from 'vitest'
import { contarPorEstado, estadoExibido, textoDoEstado } from './estado'

describe('estadoExibido', () => {
  it.each([
    ['up', 'up'],
    ['degraded', 'degraded'],
    ['down', 'down'],
    ['unknown', 'unknown'],
  ] as const)('alvo ativo mostra o estado do servidor (%s)', (estado, esperado) => {
    expect(estadoExibido(estado, true)).toBe(esperado)
  })

  // O servidor diz "unknown" também para alvo desativado: o painel distingue
  // pelo enabled, e qualquer estado de alvo inativo vira pausado.
  it.each(['up', 'degraded', 'down', 'unknown'] as const)('alvo inativo é pausado, mesmo com %s', (estado) => {
    expect(estadoExibido(estado, false)).toBe('pausado')
  })
})

describe('textoDoEstado', () => {
  it.each([
    ['up', 'No ar'],
    ['degraded', 'Degradado'],
    ['down', 'Fora do ar'],
    ['unknown', 'Sem dados'],
    ['pausado', 'Pausado'],
  ] as const)('%s é "%s"', (estado, texto) => {
    expect(textoDoEstado(estado)).toBe(texto)
  })
})

describe('contarPorEstado', () => {
  it('lista vazia conta zero em todo estado', () => {
    expect(contarPorEstado([])).toEqual({ up: 0, degraded: 0, down: 0, unknown: 0, pausado: 0 })
  })

  // O inativo conta como pausado, qualquer que seja o último estado.
  it('conta pelo estado exibido, com o inativo como pausado', () => {
    expect(
      contarPorEstado([
        { state: 'up', enabled: true },
        { state: 'up', enabled: true },
        { state: 'down', enabled: true },
        { state: 'degraded', enabled: true },
        { state: 'unknown', enabled: true },
        { state: 'down', enabled: false },
      ]),
    ).toEqual({ up: 2, degraded: 1, down: 1, unknown: 1, pausado: 1 })
  })
})
