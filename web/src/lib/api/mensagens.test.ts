import { describe, expect, it } from 'vitest'
import { textoDaCausa, textoDoErroDeCheck } from './mensagens'

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
  // blocked cobre mais que endereço interno: o sondador também recusa um host
  // que passaria a discar outro domínio.
  it('blocked não afirma um motivo só', () => {
    expect(textoDoErroDeCheck('blocked', null)).toBe('Destino bloqueado pela proteção de rede')
  })
})
