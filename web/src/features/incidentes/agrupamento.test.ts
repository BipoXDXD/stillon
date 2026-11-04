import { describe, expect, it } from 'vitest'
import { Incidente } from '@/lib/api/schemas'
import { incidente } from '@/test/fixtures'
import { agruparIncidentes, resumoDosAbertos } from './agrupamento'

const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const ACK = '2026-09-24T11:10:00Z'
const FIM = '2026-09-24T11:30:00Z'

describe('agruparIncidentes', () => {
  it('em andamento (reconhecidos inclusive) e resolvidos, na ordem da rota', () => {
    const g = agruparIncidentes([
      inc({ id: 'a' }),
      inc({ id: 'b', resolved_at: FIM }),
      inc({ id: 'c', acknowledged_at: ACK }),
      inc({ id: 'd', resolved_at: FIM, acknowledged_at: ACK }),
    ])
    expect(g.emAndamento.map((i) => i.id)).toEqual(['a', 'c'])
    expect(g.resolvidos.map((i) => i.id)).toEqual(['b', 'd'])
  })

  it('lista vazia dá grupos vazios', () => {
    expect(agruparIncidentes([])).toEqual({ emAndamento: [], resolvidos: [] })
  })
})

describe('resumoDosAbertos', () => {
  it('sem abertos, nada: o vazio positivo fala por ele', () => {
    expect(resumoDosAbertos([], true)).toBeNull()
  })

  it.each([
    [[{}], '1 em andamento.'],
    [[{}, { acknowledged_at: ACK }], '1 em andamento, 1 reconhecido.'],
    [[{ acknowledged_at: ACK }, { acknowledged_at: ACK }], '2 reconhecidos.'],
    [[{}, {}, {}], '3 em andamento.'],
  ] as const)('%j → "%s"', (ps, texto) => {
    expect(resumoDosAbertos(ps.map((p, i) => inc({ id: `i${i}`, ...p })), true)).toBe(texto)
  })

  // A consulta dos abertos tem uma página de 100, e o servidor manda
  // next_cursor sempre que a página vem cheia (len(items) == limit): com
  // exatamente 100 abertos, "mais de 100" seria falso.
  it('página cheia: não finge saber a conta', () => {
    const cem = Array.from({ length: 100 }, (_, i) => inc({ id: `i${i}` }))
    expect(resumoDosAbertos(cem, false)).toBe('100 ou mais incidentes abertos.')
  })
})
