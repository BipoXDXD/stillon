import { describe, expect, it } from 'vitest'
import { temInvisivel } from './rotulo'

// Espelha internal/rotulo/rotulo_test.go caso a caso: o painel antecipa a
// recusa do servidor, e as duas listas têm de dizer o mesmo. Os caracteres
// vão como escape para o invisível aparecer no código.
const casos: [nome: string, s: string, invisivel: boolean][] = [
  ['vazio', '', false],
  ['ascii', 'API de pagamentos', false],
  ['acento', 'Página de preço', false],
  ['emoji simples', 'Loja \u{1F6D2}', false],
  ['emoji de profissão (ZWJ)', 'Equipe \u{1F469}‍\u{1F4BB}', false],
  ['emoji com seletor de variação antes do ZWJ', '❤️‍\u{1F525}', false],
  ['emoji com tom de pele antes do ZWJ', '\u{1F468}\u{1F3FD}‍\u{1F680}', false],
  ['persa com ZWNJ', 'می‌خواهم', false],
  ['devanágari com virama antes do ZWJ', 'क्‍ष', false],

  ['controle', 'a\u0000b', true],
  ['quebra de linha', 'a\nb', true],
  ['inversão bidi', 'a‮b', true],
  ['isolamento bidi', 'a⁦b⁩', true],
  ['marca de direção', 'a‏b', true],
  ['espaço de largura zero', 'a​b', true],
  ['BOM no meio', 'a﻿b', true],
  ['hífen suave', 'a­b', true],
  ['etiqueta invisível', 'a\u{E0041}b', true],
  ['ZWJ no começo', '‍a', true],
  ['ZWJ no fim', 'a‍', true],
  ['ZWJ sozinho', '‍', true],
  ['ZWJ duplo', 'a‍‍b', true],
  ['ZWJ antes de espaço', 'a‍ b', true],
  ['ZWJ depois de espaço', 'a ‍b', true],
  ['ZWNJ antes de inversão bidi', 'a‌‮b', true],
]

describe('temInvisivel', () => {
  it.each(casos)('%s', (_nome, s, invisivel) => {
    expect(temInvisivel(s)).toBe(invisivel)
  })
})
