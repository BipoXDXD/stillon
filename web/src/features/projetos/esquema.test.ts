import { describe, expect, it } from 'vitest'
import { EsquemaDoProjeto } from './esquema'

// Espelha TestParseProjectName (internal/tenancy/project_test.go) nos casos
// que o navegador alcança. O caractere invisível fica com o servidor: o erro
// de campo dele aparece no campo (novo-projeto.test.tsx).
function erroDe(name: string): string | undefined {
  const r = EsquemaDoProjeto.safeParse({ name })
  return r.success ? undefined : r.error.issues[0]?.message
}

describe('EsquemaDoProjeto', () => {
  it('apara as pontas', () => {
    expect(EsquemaDoProjeto.parse({ name: '  Produção  ' })).toEqual({ name: 'Produção' })
  })

  it.each([
    ['vazio', ''],
    ['só espaços', '   '],
  ])('%s pede um nome', (_, name) => {
    expect(erroDe(name)).toBe('Dê um nome ao projeto.')
  })

  it('aceita 100 runas', () => {
    expect(erroDe('ç'.repeat(100))).toBeUndefined()
  })

  it('recusa 101 runas', () => {
    expect(erroDe('ç'.repeat(101))).toBe('Até 100 caracteres.')
  })

  // Conta por code point, como utf8.RuneCountInString: 100 emojis são 200
  // unidades de UTF-16 e cabem; um a mais não.
  it('conta por code point, não por unidade de UTF-16', () => {
    expect(erroDe('😀'.repeat(100))).toBeUndefined()
    expect(erroDe('😀'.repeat(101))).toBe('Até 100 caracteres.')
  })

  it('as pontas aparadas não contam no limite', () => {
    expect(erroDe(` ${'ç'.repeat(100)} `)).toBeUndefined()
  })
})
