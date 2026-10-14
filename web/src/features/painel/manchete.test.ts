import { describe, expect, it } from 'vitest'
import { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { incidente, itemDoResumo } from '@/test/fixtures'
import {
  fraseDoUltimoResolvido,
  manchete,
  metaDoPainel,
  resumoEmTexto,
  textoDaManchete,
  ultimoResolvido,
  type Manchete,
} from './manchete'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))

describe('manchete', () => {
  it('sem alvos', () => {
    expect(manchete([])).toEqual({ tipo: 'sem-alvos' })
  })

  it('fora do ar e degradado contam como problema; pausado e sem dados, não', () => {
    const itens = [alvo({ state: 'down' }), alvo({ state: 'degraded' }), alvo({ state: 'down', enabled: false }), alvo({ state: 'unknown' }), alvo()]
    expect(manchete(itens)).toEqual({ tipo: 'problemas', quantos: 2 })
  })

  it('sem problema, um ativo ainda sem dados segura o "Tudo no ar."', () => {
    expect(manchete([alvo(), alvo({ state: 'unknown' })])).toEqual({ tipo: 'aguardando', quantos: 1 })
  })

  // Review Focus 2.
  it('todos pausados', () => {
    expect(manchete([alvo({ enabled: false }), alvo({ state: 'unknown', enabled: false })])).toEqual({ tipo: 'todos-pausados' })
  })

  it('tudo no ar: todo ativo no ar, com os pausados à parte', () => {
    expect(manchete([alvo(), alvo({ enabled: false })])).toEqual({ tipo: 'tudo-no-ar' })
  })
})

describe('textoDaManchete', () => {
  it.each([
    [{ tipo: 'sem-alvos' }, 'Nenhum serviço vigiado ainda.'],
    [{ tipo: 'problemas', quantos: 1 }, 'Um serviço pede atenção.'],
    [{ tipo: 'problemas', quantos: 2 }, 'Dois serviços pedem atenção.'],
    [{ tipo: 'problemas', quantos: 10 }, 'Dez serviços pedem atenção.'],
    [{ tipo: 'problemas', quantos: 11 }, '11 serviços pedem atenção.'],
    [{ tipo: 'aguardando', quantos: 1 }, 'Um serviço ainda sem dados.'],
    [{ tipo: 'aguardando', quantos: 3 }, 'Três serviços ainda sem dados.'],
    [{ tipo: 'todos-pausados' }, 'Todos os alvos estão pausados.'],
    [{ tipo: 'tudo-no-ar' }, 'Tudo no ar.'],
  ] satisfies [Manchete, string][])('%j → "%s"', (m, texto) => {
    expect(textoDaManchete(m)).toBe(texto)
  })
})

describe('resumoEmTexto', () => {
  it('conta por gravidade, com plural, e omite o estado que não aparece', () => {
    const itens = [alvo(), alvo({ state: 'degraded' }), alvo({ state: 'degraded' }), alvo({ state: 'down' }), alvo({ state: 'unknown' }), alvo({ enabled: false })]
    expect(resumoEmTexto(itens)).toBe('1 fora do ar, 2 degradados, 1 no ar, 1 sem dados, 1 pausado')
    expect(resumoEmTexto([alvo(), alvo()])).toBe('2 no ar')
  })
})

describe('metaDoPainel', () => {
  const as = new Date(2026, 9, 14, 14, 32)

  it('conta os ativos no ar sobre o total de ativos', () => {
    expect(metaDoPainel([alvo(), alvo({ state: 'down' }), alvo({ enabled: false })], as)).toBe('Atualizado às 14:32 · 1 de 2 no ar')
  })

  it('sem ativo, não inventa um total', () => {
    expect(metaDoPainel([alvo({ enabled: false })], as)).toBe('Atualizado às 14:32 · nenhum alvo ativo')
  })
})

describe('último incidente resolvido', () => {
  // A rota ordena pela abertura: o último a terminar pode vir depois na lista.
  it('é o de resolved_at maior, não o primeiro da lista', () => {
    const cedo = inc({ id: 'i2', started_at: '2026-09-24T09:00:00Z', resolved_at: '2026-09-24T09:05:00Z' })
    const tarde = inc({ id: 'i1', started_at: '2026-09-24T08:00:00Z', resolved_at: '2026-09-24T11:00:00Z' })
    expect(ultimoResolvido([cedo, tarde])?.id).toBe('i1')
  })

  it('ignora o aberto; sem resolvido, null', () => {
    expect(ultimoResolvido([inc({ resolved_at: null })])).toBeNull()
    expect(ultimoResolvido([])).toBeNull()
  })

  it('a frase: sem resposta, nada; lista vazia, "nenhum"; senão, a data do fim', () => {
    expect(fraseDoUltimoResolvido(undefined)).toBeNull()
    expect(fraseDoUltimoResolvido([])).toBe('Nenhum incidente registrado.')
    expect(fraseDoUltimoResolvido([inc({ resolved_at: new Date(2026, 8, 24, 9, 12).toISOString() })])).toMatch(
      /^Último incidente resolvido em 24\/09\/2026,? 09:12\.$/,
    )
  })
})
