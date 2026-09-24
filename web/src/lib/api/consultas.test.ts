import { expect, it } from 'vitest'
import { chaves } from './consultas'

// Páginas de tamanhos diferentes não podem dividir uma entrada do cache: a
// de 100 do contador da navegação e uma de 20 mostrariam uma a outra.
// Sabotagem prevista: tirar o limite de chaves.incidentes.
it('o limite faz parte da chave dos incidentes', () => {
  expect(chaves.incidentes('open', undefined, 20)).not.toEqual(chaves.incidentes('open', undefined, 100))
  expect(chaves.incidentes('open', 'alvo-1', 20)).not.toEqual(chaves.incidentes('open', 'alvo-1'))
  // Tudo continua sob ['incidents'], que o reconhecer invalida.
  expect(chaves.incidentes('open', undefined, 100)[0]).toBe('incidents')
})
