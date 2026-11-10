import { describe, expect, it } from 'vitest'
import { mensagemDoCampo, textoDaCausa, textoDoErroDeCheck } from './mensagens'

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
  it('content_encoding diz que a resposta veio compactada', () => {
    expect(textoDoErroDeCheck('content_encoding', null)).toBe(
      'Resposta compactada num formato que o StillOn não lê',
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
    ['content_rule', 'forbidden', 'Com HEAD ou com o header Accept-Encoding, o conteúdo não pode ser verificado.'],
    ['content_rule.value', 'too_long', 'Até 256 caracteres.'],
    ['content_rule.value', 'invalid', 'Sem tab, quebra de linha ou outro caractere de controle.'],
    ['content_rule.json_pointer', 'too_long', 'Até 256 caracteres.'],
    ['content_rule.json_pointer', 'invalid', 'Comece com /, como /status. Use ~0 para ~ e ~1 para /.'],
    ['content_rule.value', 'required', 'Obrigatório.'],
  ])('%s %s vira texto próprio', (campo, motivo, texto) => {
    expect(mensagemDoCampo(campo, motivo)).toBe(texto)
  })
})
