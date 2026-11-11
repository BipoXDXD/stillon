/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { z } from 'zod'
import { textoDoErroDeCanal } from './mensagens'
import { Canal } from './schemas'

// O JSON real de channelResponse, escrito e conferido pelo teste Go
// TestChannelResponseKeepsThePanelContract
// (internal/httpapi/channels_contrato_internal_test.go). Nenhuma resposta é
// escrita à mão aqui: se o DTO mudar, o Go exige regravar o arquivo, e este
// teste diz se o painel aceita a forma nova.
const ARQUIVO = resolve(import.meta.dirname, '../../../../internal/httpapi/testdata/contrato-canais.json')
const CASOS = z
  .array(z.strictObject({ caso: z.string().min(1), resposta: z.record(z.string(), z.unknown()) }))
  .parse(JSON.parse(readFileSync(ARQUIVO, 'utf8')))
const RESPOSTAS = CASOS.map((c) => [c.caso, c.resposta] as const)
// O schema de produção só exige texto não vazio em `instante`; a forma que o
// painel formata como data é conferida aqui.
const Rfc3339 = z.iso.datetime({ offset: true })
const DatasDaResposta = z.object({ created_at: Rfc3339, last_failure_at: Rfc3339.nullable() })
// O texto que textoDoErroDeCanal usa para o motivo que não conhece.
const SEM_TRADUCAO = 'Erro desconhecido'
const MOTIVOS = [...new Set(CASOS.map((c) => c.resposta.last_error))].filter(
  (motivo): motivo is string => typeof motivo === 'string' && motivo !== '',
)

describe('o schema Canal aceita o que a API devolve', () => {
  it.each(RESPOSTAS)('%s', (_caso, resposta) => {
    const lido = Canal.safeParse(resposta)
    expect(lido.success ? [] : lido.error.issues).toEqual([])
  })

  // z.object descarta chave desconhecida: sem isto, um campo novo no DTO
  // passaria com o artefato regravado, sem ninguém decidir o que fazer com ele.
  it.each(RESPOSTAS)('%s: traz exatamente os campos do schema', (_caso, resposta) => {
    expect(Object.keys(resposta).sort()).toEqual(Object.keys(Canal.shape).sort())
  })

  it.each(RESPOSTAS)('%s: as datas vêm em RFC 3339', (_caso, resposta) => {
    const lidas = DatasDaResposta.safeParse(resposta)
    expect(lidas.success ? [] : lidas.error.issues).toEqual([])
  })

  it.each(MOTIVOS.filter((motivo) => motivo !== 'unknown'))('o motivo %s tem tradução própria', (motivo) => {
    expect(textoDoErroDeCanal(motivo)).not.toBe(SEM_TRADUCAO)
  })

  it('o motivo unknown chega da API e vira o texto genérico', () => {
    expect(MOTIVOS).toContain('unknown')
    expect(textoDoErroDeCanal('unknown')).toBe(SEM_TRADUCAO)
  })

  it('os casos cobrem todo kind, todo status e os dois lados de cada campo que varia', () => {
    const canais = CASOS.map((c) => Canal.parse(c.resposta))
    const valores = <T>(de: (c: Canal) => T) => new Set(canais.map(de))
    const ambos = new Set([true, false])

    expect(valores((c) => c.kind)).toEqual(new Set(Canal.shape.kind.options))
    expect(valores((c) => c.status)).toEqual(new Set(Canal.shape.status.options))
    expect(valores((c) => c.enabled)).toEqual(ambos)
    expect(valores((c) => c.last_error === null)).toEqual(ambos)
    expect(valores((c) => c.last_failure_at === null)).toEqual(ambos)
  })
})
