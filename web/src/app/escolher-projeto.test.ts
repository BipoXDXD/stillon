import { describe, expect, it } from 'vitest'
import { Projeto } from '@/lib/api/schemas'
import { projeto } from '@/test/fixtures'
import { escolherProjeto } from './escolher-projeto'

const padrao = Projeto.parse(projeto())
const a = Projeto.parse(projeto({ id: 'proj-a', name: 'A', is_default: false }))
const b = Projeto.parse(projeto({ id: 'proj-b', name: 'B', is_default: false }))

describe('escolherProjeto', () => {
  it('o lembrado que está na lista vence o padrão', () => {
    expect(escolherProjeto([padrao, a, b], 'proj-b')).toBe(b)
  })

  it('o lembrado fora da lista cai no padrão', () => {
    expect(escolherProjeto([a, padrao, b], 'proj-sumido')).toBe(padrao)
  })

  it('sem lembrado e sem padrão, o primeiro da lista', () => {
    expect(escolherProjeto([a, b], null)).toBe(a)
  })

  it('lista vazia não escolhe nada', () => {
    expect(escolherProjeto([], 'proj-a')).toBeNull()
  })
})
