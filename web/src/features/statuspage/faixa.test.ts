/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { z } from 'zod'
import { EstadoAlvo } from '@/lib/api/schemas'
import { faixaDaPagina } from './faixa'

// A frase da faixa é a da página pública (internal/httpapi/publicpage.go). Os
// casos ficam num JSON só, que este teste e o do Go leem: a prévia do painel
// e a página publicada não podem dizer coisas diferentes. strictObject: um
// campo novo no JSON tem de entrar nos dois testes, não ser ignorado aqui.
const Caso = z.strictObject({
  caso: z.string().min(1),
  itens: z.array(z.strictObject({ nome: z.string().min(1), estado: EstadoAlvo })),
  estado: EstadoAlvo,
  frase: z.string().min(1),
  detalhe: z.string(),
})
const CASOS = z.array(Caso).parse(JSON.parse(readFileSync('../internal/httpapi/testdata/faixa.json', 'utf8')))

describe('faixaDaPagina segue internal/httpapi/testdata/faixa.json', () => {
  it('o arquivo tem os nove casos, com nomes distintos', () => {
    expect(CASOS).toHaveLength(9)
    expect(new Set(CASOS.map((c) => c.caso)).size).toBe(CASOS.length)
  })

  it.each(CASOS.map((c) => [c.caso, c] as const))('%s', (_caso, c) => {
    expect(faixaDaPagina(c.itens)).toEqual({ estado: c.estado, frase: c.frase, detalhe: c.detalhe })
  })
})
