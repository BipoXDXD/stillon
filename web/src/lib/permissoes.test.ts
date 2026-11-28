import { describe, expect, it } from 'vitest'
import { pode, podeConvidarComo } from './permissoes'

// A matriz em si é conferida contra o domínio pelo teste Go
// (internal/tenancy/matriz_painel_test.go); aqui, a leitura dela.
describe('pode', () => {
  it.each([
    ['viewer', 'edit_monitors', false],
    ['member', 'edit_monitors', true],
    ['manager', 'invite_basic', true],
    ['manager', 'manage_members', false],
    ['desconhecido', 'read', true],
    ['desconhecido', 'edit_monitors', false],
  ] as const)('%s / %s → %s', (papel, permissao, esperado) => {
    expect(pode(papel, permissao)).toBe(esperado)
  })
})

describe('podeConvidarComo', () => {
  it.each([
    ['manager', 'member', true],
    ['manager', 'viewer', true],
    ['manager', 'manager', false],
    ['owner', 'owner', true],
    ['member', 'viewer', false],
  ] as const)('%s convida como %s → %s', (papel, alvo, esperado) => {
    expect(podeConvidarComo(papel, alvo)).toBe(esperado)
  })
})
