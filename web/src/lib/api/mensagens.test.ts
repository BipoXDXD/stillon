/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { z } from 'zod'
import { ApiError } from './http'
import { mensagemDe, mensagemDoCampo, textoDaCausa, textoDoErroDeCheck } from './mensagens'

// A lista de causas que o Go confere contra os prober.ErrorKind e as causas de
// tarefa (internal/notify/causas_test.go). Um kind sem texto aqui cairia em
// "Falha", o mesmo que um valor fora do enum.
const CAUSAS = z
  .array(z.string().min(1))
  .min(1)
  .parse(JSON.parse(readFileSync(resolve(import.meta.dirname, '../../../../internal/notify/testdata/causas.json'), 'utf8')))
const SEM_TEXTO = textoDoErroDeCheck('fora_do_enum', null)

describe('as causas que o Go conhece', () => {
  // unknown é o residual: na tela ele é a mesma "Falha" de um valor que não
  // está no enum.
  it.each(CAUSAS.filter((c) => c !== 'unknown'))('%s tem texto próprio', (causa) => {
    expect(textoDoErroDeCheck(causa, null)).not.toBe(SEM_TEXTO)
  })
})

// Review Focus 7: um incidente sem causa não pode virar um espaço em branco
// entre dois "·" na tela.
describe('textoDaCausa', () => {
  it.each([
    ['timeout', 'Tempo esgotado'],
    ['conn_refused', 'Conexão recusada'],
    ['um_kind_novo', 'Falha'],
    ['', 'Causa não registrada'],
  ])('%j vira "%s"', (causa, texto) => {
    expect(textoDaCausa(causa)).toBe(texto)
  })
})

describe('textoDoErroDeCheck', () => {
  // Um br ou zstd que o sondador não pediu não é "resposta grande demais".
  it('content_encoding diz que a resposta veio comprimida', () => {
    expect(textoDoErroDeCheck('content_encoding', null)).toBe(
      'Resposta comprimida num formato que o StillOn não lê',
    )
  })

  // blocked cobre mais que endereço interno: o sondador também recusa um host
  // que passaria a discar outro domínio.
  it('blocked não afirma um motivo só', () => {
    expect(textoDoErroDeCheck('blocked', null)).toBe('Destino bloqueado pela proteção de rede')
  })
})

// Sabotagem: tirar o texto de content_mismatch → "Falha" aparece.
describe('regra de conteúdo', () => {
  it('content_mismatch diz que o conteúdo não bateu', () => {
    expect(textoDoErroDeCheck('content_mismatch', null)).toBe('Conteúdo diferente do esperado')
  })

  it('body_limit diz que a resposta não coube na conferência', () => {
    expect(textoDoErroDeCheck('body_limit', null)).toBe('Resposta grande demais para conferir o conteúdo')
  })

  it.each([
    ['content_rule', 'forbidden', 'Para verificar o conteúdo, use GET ou POST e tire os headers Accept-Encoding e Range.'],
    ['content_rule.value', 'too_long', 'Até 256 caracteres.'],
    ['content_rule.value', 'invalid', 'Sem tab, quebra de linha ou outro caractere de controle.'],
    ['content_rule.json_pointer', 'too_long', 'Até 256 caracteres.'],
    ['content_rule.json_pointer', 'invalid', 'Comece com /, como /status, sem tab ou quebra de linha. No ponteiro, escreva ~1 para uma barra e ~0 para um til.'],
    ['content_rule.value', 'required', 'Obrigatório.'],
  ])('%s %s vira texto próprio', (campo, motivo, texto) => {
    expect(mensagemDoCampo(campo, motivo)).toBe(texto)
  })

  it('o 409 do teto de regras por host tem texto próprio, sem o host', () => {
    expect(mensagemDe(new ApiError(409, 'content_rule_host_limit_reached', ''))).toBe(
      'Limite de regras de conteúdo para este host atingido.',
    )
  })
})
