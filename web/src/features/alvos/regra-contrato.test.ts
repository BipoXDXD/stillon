/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { z } from 'zod'
import { esquemaAlvo, type ValoresDoAlvo } from './esquema'

// Os casos da regra de conteúdo que o servidor também roda
// (TestContentRuleCasesSharedWithThePanel, em
// internal/targets/contentrule_painel_test.go). O servidor é a autoridade: se
// o espelho daqui divergir, o erro só chegaria pelo 400, depois do envio.
const ARQUIVO = resolve(import.meta.dirname, '../../../../internal/targets/testdata/regra-de-conteudo.json')
const CASOS = z
  .array(
    z.strictObject({
      caso: z.string().min(1),
      modo: z.enum(['contains', 'not_contains', 'json_equals']),
      valor: z.string(),
      valorAte: z.number().int().optional(),
      ponteiro: z.string().optional(),
      ponteiroAte: z.number().int().optional(),
      header: z.string().optional(),
      erro: z.enum(['', 'valor', 'ponteiro', 'regra']),
    }),
  )
  .min(1)
  .parse(JSON.parse(readFileSync(ARQUIVO, 'utf8')))

// O mesmo preenchimento do Go: 1 caractere, 2 unidades em UTF-16.
const PREENCHIMENTO = '😀'
const CAMPO_DO_ERRO = { valor: 'valorDaRegra', ponteiro: 'ponteiroDaRegra', regra: 'modoDaRegra' } as const

function completar(s: string, ate = 0): string {
  return s + PREENCHIMENTO.repeat(Math.max(0, ate - [...s].length))
}

function entrada(c: (typeof CASOS)[number]): ValoresDoAlvo {
  return {
    name: 'API', url: 'https://api.exemplo.com/health', method: 'GET',
    intervaloEscolhido: '60', interval_sec: 60, timeout_ms: 10000,
    expected_status: 200, lentidao: 'padrao', degraded_threshold_ms: 2000,
    enabled: true, modoHeaders: 'substituir',
    headers: c.header === undefined ? [] : [{ nome: c.header, valor: 'x' }],
    tagsTexto: '', modoDaRegra: c.modo,
    valorDaRegra: completar(c.valor, c.valorAte),
    ponteiroDaRegra: completar(c.ponteiro ?? '', c.ponteiroAte),
  }
}

describe('a regra de conteúdo julga como o servidor', () => {
  it.each(CASOS.map((c) => [c.caso, c] as const))('%s', (_caso, c) => {
    const r = esquemaAlvo.safeParse(entrada(c))
    const campos = r.success ? [] : r.error.issues.map((e) => e.path.join('.'))
    expect(campos).toEqual(c.erro === '' ? [] : [CAMPO_DO_ERRO[c.erro]])
  })
})
