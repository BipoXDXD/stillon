/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { render, within } from '@testing-library/react'
import { createElement } from 'react'
import { describe, expect, it } from 'vitest'
import { ControleSegmentado } from '@/components/ui/controle-segmentado'
import { VISUAL_DA_FAIXA } from '@/features/alvos/FaixaDeEstado'

// Contraste dos tokens de cor, lidos do próprio index.css, nos dois temas
// (spec do redesign, seção Testes). Os tokens são OKLCH: o teste converte para
// sRGB e aplica a fórmula do WCAG 2, sem dependência nova. O axe confere texto
// renderizado, mas não vê o limite de um campo nem uma barra de gráfico
// (WCAG 1.4.11): este teste cobre os dois lados.
const css = readFileSync('src/index.css', 'utf8')

type Tema = 'claro' | 'escuro'
type Oklch = { l: number; c: number; h: number }

function bloco(seletor: string): string {
  const inicio = css.indexOf(`${seletor} {`)
  if (inicio === -1) throw new Error(`bloco "${seletor} {" ausente no index.css`)
  return css.slice(inicio, css.indexOf('}', inicio))
}

function declaracoes(seletor: string): Map<string, string> {
  return new Map(Array.from(bloco(seletor).matchAll(/--([\w-]+):\s*([^;]+);/g), (m) => [m[1] ?? '', (m[2] ?? '').trim()]))
}

// O .dark fica no mesmo <html> que o :root: o que ele não redefine vem do
// :root, e um var() resolve com os valores do tema em vigor.
const TOKENS: Record<Tema, Map<string, string>> = {
  claro: declaracoes(':root'),
  escuro: new Map([...declaracoes(':root'), ...declaracoes('.dark')]),
}

const OKLCH = /^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)$/
const VAR = /^var\(--([\w-]+)\)$/

function cor(tema: Tema, nome: string, caminho: string[] = []): Oklch {
  if (caminho.includes(nome)) throw new Error(`ciclo de var(): --${[...caminho, nome].join(' → --')}`)
  const valor = TOKENS[tema].get(nome)
  if (valor === undefined) throw new Error(`token --${nome} ausente no tema ${tema}`)
  const referencia = VAR.exec(valor)
  if (referencia) return cor(tema, referencia[1] ?? '', [...caminho, nome])
  const m = OKLCH.exec(valor)
  if (!m) throw new Error(`--${nome} no tema ${tema} não é oklch(L C H) nem var(--x): ${valor}`)
  return { l: Number(m[1]), c: Number(m[2]), h: Number(m[3]) }
}

// OKLCH → OKLab → sRGB linear, com as matrizes de Björn Ottosson ("A
// perceptual color space for image processing", 2020), as mesmas do CSS
// Color 4. Um canal fora do gamut sRGB é cortado em [0, 1].
function srgbLinear({ l, c, h }: Oklch): [number, number, number] {
  const angulo = (h * Math.PI) / 180
  const a = c * Math.cos(angulo)
  const b = c * Math.sin(angulo)
  const longo = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const medio = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const curto = (l - 0.0894841775 * a - 1.291485548 * b) ** 3
  const cortar = (x: number) => Math.min(1, Math.max(0, x))
  return [
    cortar(4.0767416621 * longo - 3.3077115913 * medio + 0.2309699292 * curto),
    cortar(-1.2684380046 * longo + 2.6097574011 * medio - 0.3413193965 * curto),
    cortar(-0.0041960863 * longo - 0.7034186147 * medio + 1.707614701 * curto),
  ]
}

// Luminância relativa do WCAG 2; o sRGB aqui já está linear.
function luminancia(c: Oklch): number {
  const [r, g, b] = srgbLinear(c)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

// Fórmula do WCAG 2 (definição de contrast ratio).
function contraste(a: Oklch, b: Oklch): number {
  const [la, lb] = [luminancia(a), luminancia(b)]
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)
}

const FUNDOS = ['fundo', 'superficie', 'painel']
// Texto e ícones: AA de texto (4,5:1) sobre todo fundo do painel.
const TEXTOS = ['texto', 'suave', 'apagado', 'primaria', 'realce', 'perigo', 'estado-no-ar', 'estado-degradado', 'estado-fora', 'estado-sem-dados']
// Texto sobre fundo tintado: o selo de estado sobre o tom 100, o item ativo
// e o selo de reconhecido sobre a primária 100, os botões sólidos, o erro do
// formulário.
const TONS_100 = ['estado-no-ar-100', 'estado-degradado-100', 'estado-fora-100', 'estado-sem-dados-100']
const SOBRE_TINTA: [string, string][] = [
  ['estado-no-ar', 'estado-no-ar-100'],
  ['estado-degradado', 'estado-degradado-100'],
  ['estado-fora', 'estado-fora-100'],
  ['estado-sem-dados', 'estado-sem-dados-100'],
  ['perigo', 'estado-fora-100'],
  ['primaria', 'primaria-100'],
  ['texto', 'primaria-100'],
  // A contagem do botão de filtro ligado (features/painel/BarraDeFiltros).
  ['suave', 'primaria-100'],
  ['primaria-texto', 'primaria'],
  ['perigo-texto', 'perigo'],
  // A faixa de estado do detalhe (PR 4): título em texto e detalhe em suave
  // sobre o tom 100. O apagado não entra: no escuro fica em 4,1:1.
  ...['texto', 'suave'].flatMap((t) => TONS_100.map((f): [string, string] => [t, f])),
  // O tom 700 é o texto do mesmo matiz sobre o tom 100 (RUI, texto sobre fundo
  // colorido), no lugar do cinza suave.
  ...TONS_100.map((f): [string, string] => [f.replace(/-100$/, '-700'), f]),
]
// Componentes de UI (1.4.11, 3:1): o limite dos controles, o anel de foco
// (também sobre o item ativo da navegação) e o fundo do botão primário.
const COMPONENTES: [string, string][] = [
  ...['borda-controle', 'foco', 'primaria'].flatMap((c) => FUNDOS.map((f): [string, string] => [c, f])),
  ['foco', 'primaria-100'],
  // O Reconhecer da faixa é primário, com o foco por cima do tom 100. O
  // secundário não serviria: a borda de controle fica em 2,7:1 no escuro.
  ...['primaria', 'foco'].flatMap((c) => TONS_100.map((f): [string, string] => [c, f])),
]

describe.each(['claro', 'escuro'] as const)('contraste dos tokens, tema %s', (tema) => {
  const par = (a: string, b: string) => contraste(cor(tema, a), cor(tema, b))

  it.each(TEXTOS.flatMap((t) => FUNDOS.map((f) => [t, f])))('texto --%s sobre --%s chega a 4,5:1', (t, f) => {
    expect(par(t, f)).toBeGreaterThanOrEqual(4.5)
  })

  it.each(SOBRE_TINTA)('texto --%s sobre --%s chega a 4,5:1', (t, f) => {
    expect(par(t, f)).toBeGreaterThanOrEqual(4.5)
  })

  it.each(COMPONENTES)('componente --%s sobre --%s chega a 3:1', (c, f) => {
    expect(par(c, f)).toBeGreaterThanOrEqual(3)
  })
})

// O controle segmentado lido do próprio componente: as cores vêm das classes
// que ele põe no DOM, e não de uma lista à parte, que seguiria verde com a
// classe trocada. O item ativo se separa do trilho pela superfície, mas
// --superficie sobre --painel dá 1,2:1 nos dois temas (e o escuro não tem
// sombra): quem cumpre o 3:1 do estado (WCAG 1.4.11) é a borda do item.
describe.each(['claro', 'escuro'] as const)('contraste do ControleSegmentado, tema %s', (tema) => {
  function montar() {
    const { container } = render(
      createElement(ControleSegmentado<string>, {
        rotulo: 'Filtro',
        opcoes: [
          { valor: 'a', rotulo: 'Ativo' },
          { valor: 'b', rotulo: 'Inativo' },
        ],
        valor: 'a',
        aoMudar: () => undefined,
      }),
    )
    const tela = within(container)
    return {
      trilho: tela.getByRole('radiogroup'),
      ativo: tela.getByRole('radio', { name: 'Ativo' }),
      inativo: tela.getByRole('radio', { name: 'Inativo' }),
    }
  }

  // "text-suave" → "suave". Só as classes do estado normal, sem variante
  // (hover:), e só as que são cor: text-sm não é um token.
  function tokenDaClasse(elemento: HTMLElement, prefixo: 'bg' | 'text' | 'border'): string {
    const tokens = Array.from(elemento.classList, (c) => (c.startsWith(`${prefixo}-`) ? c.slice(prefixo.length + 1) : '')).filter((t) =>
      TOKENS[tema].has(t),
    )
    expect(tokens, `a classe ${prefixo}-* de cor em "${elemento.className}"`).toHaveLength(1)
    return tokens[0] ?? ''
  }
  const par = (a: string, b: string) => contraste(cor(tema, a), cor(tema, b))

  it('o texto do item inativo chega a 4,5:1 sobre o trilho, também no hover', () => {
    const { trilho, inativo } = montar()
    expect(par(tokenDaClasse(inativo, 'text'), tokenDaClasse(trilho, 'bg'))).toBeGreaterThanOrEqual(4.5)
    expect(inativo).toHaveClass('hover:text-texto')
    expect(par('texto', tokenDaClasse(trilho, 'bg'))).toBeGreaterThanOrEqual(4.5)
  })

  it('o texto do item ativo chega a 4,5:1 sobre o próprio item', () => {
    const { ativo } = montar()
    expect(par(tokenDaClasse(ativo, 'text'), tokenDaClasse(ativo, 'bg'))).toBeGreaterThanOrEqual(4.5)
  })

  it('o limite do item ativo chega a 3:1 contra o trilho e contra o item', () => {
    const { trilho, ativo } = montar()
    const limite = tokenDaClasse(ativo, 'border')
    expect(par(limite, tokenDaClasse(trilho, 'bg'))).toBeGreaterThanOrEqual(3)
    expect(par(limite, tokenDaClasse(ativo, 'bg'))).toBeGreaterThanOrEqual(3)
  })

  it('o anel de foco chega a 3:1 sobre o trilho e sobre o item ativo', () => {
    const { trilho, ativo } = montar()
    expect(par('foco', tokenDaClasse(trilho, 'bg'))).toBeGreaterThanOrEqual(3)
    expect(par('foco', tokenDaClasse(ativo, 'bg'))).toBeGreaterThanOrEqual(3)
  })
})

// Os tons de um estado formam uma escala: o 700 é do mesmo matiz que o 100
// sobre o qual fica, e não um cinza. Matiz igual, em graus.
describe.each(['claro', 'escuro'] as const)('escala dos estados, tema %s', (tema) => {
  it.each(TONS_100)('o 700 de --%s tem o mesmo matiz', (fundo) => {
    expect(cor(tema, fundo.replace(/-100$/, '-700')).h).toBe(cor(tema, fundo).h)
  })
})

// A faixa de estado do detalhe lida da própria tabela do componente: o texto
// secundário é do mesmo matiz que o fundo, com 4,5:1. Uma classe trocada por
// text-suave reprova aqui (o cinza não tem o matiz do fundo).
describe.each(['claro', 'escuro'] as const)('a faixa de estado, tema %s', (tema) => {
  const token = (classe: string, prefixo: 'bg' | 'text') => classe.slice(prefixo.length + 1)
  const doTema = (nome: string) => (nome.startsWith('estado-') || TOKENS[tema].has(nome) ? nome : `estado-${nome}`)

  it.each(Object.entries(VISUAL_DA_FAIXA))('%s: o texto secundário é do matiz do fundo, com 4,5:1', (_, visual) => {
    const fundo = cor(tema, doTema(token(visual.fundo, 'bg')))
    const texto = cor(tema, doTema(token(visual.secundario, 'text')))
    expect(texto.h).toBe(fundo.h)
    expect(contraste(texto, fundo)).toBeGreaterThanOrEqual(4.5)
  })
})

describe('tokens do index.css', () => {
  it('as cores dos dois temas são OKLCH, sem hex', () => {
    expect(bloco(':root')).not.toMatch(/#[0-9a-f]{3,8}\b/i)
    expect(bloco('.dark')).not.toMatch(/#[0-9a-f]{3,8}\b/i)
  })

  // Cada cor do Tailwind (bg-fora-100, text-apagado…) aponta para um token
  // que existe: um nome errado no @theme inline daria uma classe sem cor.
  it('todo var() do @theme inline aponta para um token do :root', () => {
    const referencias = Array.from(bloco('@theme inline').matchAll(/var\(--([\w-]+)\)/g), (m) => m[1] ?? '')
    expect(referencias.length).toBeGreaterThan(0)
    expect(referencias.filter((r) => !TOKENS.claro.has(r))).toEqual([])
  })
})

describe('a conversão e a fórmula do teste', () => {
  // Valores de referência: preto × branco = 21:1; #767676, o cinza mais
  // claro com 4,5:1 sobre branco (4,54:1), é oklch(0.5658 0 0); o vermelho
  // sRGB puro, oklch(0.62796 0.25768 29.2339), tem luminância 0,2126.
  it('confere com os valores de referência', () => {
    const branco = { l: 1, c: 0, h: 0 }
    const cinza = { l: 0.5658, c: 0, h: 0 }
    expect(contraste({ l: 0, c: 0, h: 0 }, branco)).toBeCloseTo(21, 5)
    expect(contraste(cinza, branco)).toBeCloseTo(4.54, 2)
    expect(contraste(branco, cinza)).toBeCloseTo(4.54, 2)
    expect(luminancia({ l: 0.62796, c: 0.25768, h: 29.2339 })).toBeCloseTo(0.2126, 3)
  })
})
