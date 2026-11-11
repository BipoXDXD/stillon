import { describe, expect, it } from 'vitest'
import { ApiError } from './http'
import { errosDosCampos } from './erros-de-campo'

const mapear = (fe: { field: string; reason: string }) =>
  fe.field.startsWith('x') ? { campo: fe.field.toUpperCase(), mensagem: fe.reason } : undefined

const comCampos = (...fields: { field: string; reason: string }[]) =>
  new ApiError(400, 'validation_failed', 'm', fields)

describe('errosDosCampos', () => {
  it.each([
    ['sem fields', comCampos(), []],
    ['campo desconhecido', comCampos({ field: 'y', reason: 'invalid' }), []],
    ['um campo', comCampos({ field: 'x1', reason: 'invalid' }), [{ campo: 'X1', mensagem: 'invalid' }]],
    [
      'vários campos, na ordem do servidor, sem o desconhecido',
      comCampos({ field: 'x2', reason: 'required' }, { field: 'y', reason: 'invalid' }, { field: 'x1', reason: 'too_long' }),
      [
        { campo: 'X2', mensagem: 'required' },
        { campo: 'X1', mensagem: 'too_long' },
      ],
    ],
    ['erro que não é 400, sem fields', new ApiError(500, 'internal_error', 'm'), []],
    ['erro que não é ApiError', new Error('x1'), []],
    ['valor que não é erro', 'x1', []],
  ])('%s', (_nome, err, esperado) => {
    expect(errosDosCampos(err, mapear)).toEqual(esperado)
  })
})
