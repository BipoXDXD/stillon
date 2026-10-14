/// <reference types="node" />
import { readFileSync, statSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// As fontes do painel (spec do redesign, "Decisões técnicas"): woff2 variável,
// subconjunto latino, em public/fonts com a licença OFL ao lado. O Vite copia
// public/ sem hash, e a URL /fonts/… fica estável também para a página
// pública. scripts/gerar-fontes.sh gera os arquivos; este teste confere o
// disco contra o que o index.css promete.
const css = readFileSync('src/index.css', 'utf8')
const faces = Array.from(css.matchAll(/@font-face\s*\{([^}]*)\}/g), (m) => m[1] ?? '')

function propriedade(face: string, nome: string): string {
  const m = new RegExp(`${nome}:\\s*([^;]+);`).exec(face)
  if (!m) throw new Error(`@font-face sem ${nome}`)
  return (m[1] ?? '').trim()
}

function arquivoDa(face: string): string {
  const src = propriedade(face, 'src')
  const url = /^url\('\/fonts\/([\w-]+\.woff2)'\) format\('woff2'\)$/.exec(src)
  if (!url) throw new Error(`src fora do formato url('/fonts/x.woff2') format('woff2'): ${src}`)
  return `public/fonts/${url[1] ?? ''}`
}

// O subconjunto gerado tem 69.392 bytes (Fraunces) e 105.352 (Inter) com o
// fonttools 4.63.0. O teto pega um arquivo que perdeu o corte de caracteres.
const TETO_EM_BYTES = 128 * 1024
const POR_FAMILIA = faces.map((f) => [propriedade(f, 'font-family'), f] as const)

describe('@font-face do index.css', () => {
  it('declara a Fraunces e a Inter', () => {
    expect(POR_FAMILIA.map(([familia]) => familia)).toEqual(["'Fraunces'", "'Inter'"])
  })

  it.each(POR_FAMILIA)('%s aponta para um woff2 de verdade em public/fonts, dentro do teto', (_familia, face) => {
    const caminho = arquivoDa(face)
    expect(readFileSync(caminho).subarray(0, 4).toString('latin1')).toBe('wOF2')
    expect(statSync(caminho).size).toBeLessThanOrEqual(TETO_EM_BYTES)
  })

  it.each(POR_FAMILIA)('%s é variável (100 a 900) e não esconde o texto enquanto carrega', (_familia, face) => {
    expect(propriedade(face, 'font-weight')).toBe('100 900')
    expect(propriedade(face, 'font-display')).toBe('swap')
  })

  // As setas de "← Painel" e "Ver incidente →" entram no subconjunto; o
  // resto (grego, cirílico, emoji) cai na família genérica.
  it.each(POR_FAMILIA)('%s cobre o latim e as setas', (_familia, face) => {
    const faixa = propriedade(face, 'unicode-range')
    expect(faixa).toContain('U+0000-00FF')
    expect(faixa).toContain('U+2190-2193')
  })
})

describe('pilhas de fonte do tema', () => {
  // Sem a família genérica no fim, um caractere fora do subconjunto não
  // teria para onde cair.
  it.each([
    ['--font-sans', "'Inter', ui-sans-serif, system-ui, sans-serif"],
    ['--font-display', "'Fraunces', ui-serif, Georgia, serif"],
  ])('%s termina numa família genérica', (token, pilha) => {
    expect(css).toContain(`${token}: ${pilha};`)
  })
})

describe('licenças', () => {
  it.each([
    ['OFL-Fraunces.txt', 'Copyright 2018 The Fraunces Project Authors'],
    ['OFL-Inter.txt', 'Copyright 2020 The Inter Project Authors'],
  ])('public/fonts/%s acompanha a fonte', (arquivo, copyright) => {
    const texto = readFileSync(`public/fonts/${arquivo}`, 'utf8')
    expect(texto).toContain(copyright)
    expect(texto).toContain('SIL Open Font License, Version 1.1')
  })
})
