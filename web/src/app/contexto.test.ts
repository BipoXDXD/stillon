import { expect, it } from 'vitest'
import { ApiError } from '@/lib/api/http'
import { criarClienteDeConsultas, podeRepetir } from './contexto'

// Sabotagem prevista: voltar à regra antiga,
// "!(e instanceof ApiError && e.status >= 400 && e.status < 500)" → o erro
// cru e o 200 fora do contrato passam a ser repetidos.
it('só rede caída e 5xx são repetidos', () => {
  expect(podeRepetir(new ApiError(0, 'network', ''))).toBe(true)
  expect(podeRepetir(new ApiError(502, 'unexpected', ''))).toBe(true)
  expect(podeRepetir(new ApiError(500, 'internal_error', ''))).toBe(true)
  expect(podeRepetir(new ApiError(200, 'unexpected', ''))).toBe(false)
  expect(podeRepetir(new ApiError(404, 'not_found', ''))).toBe(false)
  expect(podeRepetir(new ApiError(429, 'rate_limited', ''))).toBe(false)
  expect(podeRepetir(new TypeError('x'))).toBe(false)
})

it('o QueryClient do painel usa a regra, e para na segunda repetição', () => {
  const retry = criarClienteDeConsultas().getDefaultOptions().queries?.retry
  if (typeof retry !== 'function') throw new Error('retry não é função')
  const e500 = new ApiError(500, 'internal_error', '')
  expect(retry(0, e500)).toBe(true)
  expect(retry(1, e500)).toBe(true)
  expect(retry(2, e500)).toBe(false)
  expect(retry(0, new ApiError(200, 'unexpected', ''))).toBe(false)
})
