import { describe, expect, it } from 'vitest'
import { estadoExibido, textoDoEstado } from './estado'

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
