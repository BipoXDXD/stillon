import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// Contraste dos tokens de cor, lidos do próprio index.css, nos dois temas.
// O axe confere texto renderizado, mas não vê o limite de um campo nem uma
// barra de gráfico (WCAG 1.4.11): este teste cobre os dois lados.
const css = readFileSync('src/index.css', 'utf8')

type Tema = 'claro' | 'escuro'
const SELETORES: Record<Tema, string> = { claro: ':root', escuro: '.dark' }

function tokens(tema: Tema): Map<string, string> {
  const inicio = css.indexOf(`${SELETORES[tema]} {`)
  const bloco = css.slice(inicio, css.indexOf('}', inicio))
  return new Map(Array.from(bloco.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6})\s*;/gi), (m) => [m[1]!, m[2]!]))
}

function luminancia(hex: string): number {
  const canal = (i: number) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * canal(1) + 0.7152 * canal(3) + 0.0722 * canal(5)
}

// Fórmula do WCAG 2 (definição de contrast ratio).
function contraste(a: string, b: string): number {
  const [claro, escuro] = [luminancia(a), luminancia(b)].sort((x, y) => y - x) as [number, number]
  return (claro + 0.05) / (escuro + 0.05)
}

const FUNDOS = ['fundo', 'superficie', 'painel']
// Texto e ícones de estado: AA de texto (4,5:1) sobre todo fundo do painel.
const TEXTOS = ['texto', 'suave', 'realce', 'perigo', 'estado-no-ar', 'estado-degradado', 'estado-fora', 'estado-sem-dados']
// Componentes de UI e objetos gráficos (1.4.11, 3:1): o limite dos controles,
// o anel de foco, o fundo do botão primário. As barras do minigráfico usam as
// cores de estado, já cobertas pelo 4,5:1 acima.
const COMPONENTES = ['borda-controle', 'foco', 'primaria']

describe.each(['claro', 'escuro'] as const)('contraste dos tokens, tema %s', (tema) => {
  const t = tokens(tema)
  const cor = (nome: string) => {
    const valor = t.get(nome)
    if (!valor) throw new Error(`token --${nome} ausente no tema ${tema}`)
    return valor
  }

  it.each(TEXTOS.flatMap((texto) => FUNDOS.map((fundo) => [texto, fundo])))('texto --%s sobre --%s chega a 4,5:1', (texto, fundo) => {
    expect(contraste(cor(texto), cor(fundo))).toBeGreaterThanOrEqual(4.5)
  })

  it('texto do botão primário sobre a primária chega a 4,5:1', () => {
    expect(contraste(cor('primaria-texto'), cor('primaria'))).toBeGreaterThanOrEqual(4.5)
  })

  it.each(COMPONENTES.flatMap((comp) => FUNDOS.map((fundo) => [comp, fundo])))('componente --%s sobre --%s chega a 3:1', (comp, fundo) => {
    expect(contraste(cor(comp), cor(fundo))).toBeGreaterThanOrEqual(3)
  })
})

describe('a fórmula de contraste do teste', () => {
  // Valores de referência publicados: preto x branco = 21:1; #767676 é o
  // cinza mais claro com 4,5:1 sobre branco (4,54:1).
  it('confere com os valores de referência', () => {
    expect(contraste('#000000', '#ffffff')).toBeCloseTo(21, 5)
    expect(contraste('#767676', '#ffffff')).toBeCloseTo(4.54, 2)
    expect(contraste('#ffffff', '#767676')).toBeCloseTo(4.54, 2)
  })
})
