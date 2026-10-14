import { describe, expect, it } from 'vitest'
import { cn } from './utils'

// O tailwind-merge só conhece a escala padrão do Tailwind: sem a extensão,
// "text-meta" passa por cor e some ao lado de "text-suave".
describe('cn com as classes do sistema', () => {
  it('text-meta é tamanho de fonte: convive com uma cor de texto', () => {
    expect(cn('text-meta', 'text-suave')).toBe('text-meta text-suave')
  })

  it('text-meta substitui outro tamanho', () => {
    expect(cn('text-sm', 'text-meta')).toBe('text-meta')
  })

  it('rounded-padrao e rounded-full são o mesmo grupo: vence o último', () => {
    expect(cn('rounded-padrao', 'rounded-full')).toBe('rounded-full')
  })
})
