/// <reference types="node" />
import { readdirSync, readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// Fitness function do sistema visual do redesign (DI 2 a 4): espaçamento só
// na escala 4/8/12/16/24/32/48/64 (classes 1, 2, 3, 4, 6, 8, 12, 16), um raio
// (rounded-padrao) e pílulas (rounded-full), pesos 400 e 600, sombras só de
// dois níveis (sombra-nivel-1 e -2) e cor só por token. Um arquivo entra na
// lista quando o PR que o redesenha chega; o PR 8 troca a lista por todo o src.
const ARQUIVOS_NO_SISTEMA = [
  'src/app/BotaoDeTema.tsx',
  'src/app/Layout.tsx',
  'src/components/Marca.tsx',
  'src/components/Minigrafico.tsx',
  'src/components/Titulo.tsx',
  'src/components/SeloDeEstado.tsx',
  'src/components/SeloDoIncidente.tsx',
  'src/components/ui/avisos.tsx',
  'src/components/ui/botao.tsx',
  'src/components/ui/campo.tsx',
  'src/components/ui/controle-segmentado.tsx',
  'src/components/ui/dialogo.tsx',
  'src/features/painel/EsqueletoDoPainel.tsx',
  'src/features/alvos/GraficoDeLatencia.tsx',
  'src/features/painel/PaginaPainel.tsx',
]

const ESCALA = new Set(['0', 'auto', '1', '2', '3', '4', '6', '8', '12', '16'])
const ESPACO = String.raw`(?:p[xytrblse]?|m[xytrblse]?|gap(?:-[xy])?|space-[xy])`
const CORES_DO_TAILWIND =
  'black|white|slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose'

type Regra = { nome: string; padrao: RegExp; aceita?: (achado: RegExpExecArray) => boolean }

const REGRAS: Regra[] = [
  {
    nome: 'espaçamento fora da escala',
    // O valor começa por minúscula ou dígito: as classes do Tailwind nunca
    // começam por maiúscula, e assim o locale 'pt-BR' não vira "pt-BR" de espaço.
    padrao: new RegExp(String.raw`(?<![\w-])-?${ESPACO}-([a-z\d.][\w.]*)(?![\w./-])`, 'g'),
    aceita: (a) => ESCALA.has(a[1] ?? ''),
  },
  {
    nome: 'valor arbitrário de espaço, fonte, raio, sombra ou cor',
    padrao: new RegExp(
      String.raw`(?<![\w-])-?(?:${ESPACO}|text|rounded(?:-[a-z]{1,2})?|shadow|bg|border|ring|outline|fill|stroke|accent)-\[[^\]]*\]`,
      'g',
    ),
  },
  {
    nome: 'raio fora do sistema',
    padrao: /(?<![\w-])rounded(?:-\w+)*(?![\w-])/g,
    aceita: (a) => a[0] === 'rounded-padrao' || a[0] === 'rounded-full',
  },
  { nome: 'peso fora de 400 e 600', padrao: /(?<![\w-])font-(?:thin|extralight|light|medium|bold|extrabold|black)(?![\w-])/g },
  { nome: 'sombra do Tailwind', padrao: /(?<![\w-])shadow(?:-\w+)?(?![\w-])/g },
  {
    nome: 'cor fora dos tokens',
    padrao: new RegExp(String.raw`(?<![\w-])(?:bg|text|border|ring|outline|fill|stroke|accent)-(?:${CORES_DO_TAILWIND})(?![\w-])`, 'g'),
  },
  { nome: 'tamanho de fonte fora da escala', padrao: /(?<![\w-])text-[5-9]xl(?![\w-])/g },
]

function violacoes(codigo: string): string[] {
  return REGRAS.flatMap((r) =>
    Array.from(codigo.matchAll(r.padrao))
      .filter((achado) => !r.aceita?.(achado))
      .map((achado) => `${r.nome}: ${achado[0]}`),
  )
}

describe('o detector do sistema', () => {
  it.each(['p-5', 'space-y-1.5', 'py-0.5', 'text-[0.65rem]', 'px-[13px]', 'rounded-md', 'focus:rounded', 'font-bold', 'font-medium', 'shadow-lg', 'shadow', 'bg-black/60', 'text-6xl'])(
    'reprova %s',
    (classe) => {
      expect(violacoes(`className="${classe}"`)).not.toEqual([])
    },
  )

  it.each(['p-4', 'gap-6', 'mx-auto', '-mt-2', 'lg:py-12', 'rounded-padrao', 'rounded-full', 'font-semibold', 'sombra-nivel-1', 'bg-fora-100', 'text-3xl', 'min-h-11', 'size-11', 'max-w-[100rem]'])(
    'aceita %s',
    (classe) => {
      expect(violacoes(`className="${classe}"`)).toEqual([])
    },
  )

  it("aceita o locale 'pt-BR' fora de classe", () => {
    expect(violacoes(`new Intl.DateTimeFormat('pt-BR', { hour: '2-digit' })`)).toEqual([])
  })
})

describe.each(ARQUIVOS_NO_SISTEMA)('%s', (arquivo) => {
  it('segue o sistema visual (escala, raio, pesos, sombras e tokens)', () => {
    expect(violacoes(readFileSync(arquivo, 'utf8'))).toEqual([])
  })
})

// O contrato do index.css que os PRs seguintes usam.
describe('o index.css publica a escala e os utilitários', () => {
  const css = readFileSync('src/index.css', 'utf8')
  it.each([
    '--text-meta: 0.8125rem;',
    '--text-3xl: 1.75rem;',
    '--radius-padrao: 0.5rem;',
    '@utility sombra-nivel-1 {',
    '@utility sombra-nivel-2 {',
    '@utility rotulo-de-grupo {',
  ])('%s', (trecho) => {
    expect(css).toContain(trecho)
  })
})

// O vermelho sólido é só do botão que confirma uma ação destrutiva; em toda
// linha de lista, ele grita mais que o problema que a tela quer mostrar.
// Cada uso de tipo="perigo" está contado por arquivo; um "Apagar" vermelho
// numa linha de lista muda a contagem. Os dois usos: o Confirmar do diálogo
// e o envio do Desativar 2FA, que já é o passo de confirmação, dentro do
// diálogo.
describe('vermelho sólido só na confirmação', () => {
  const PERIGO = /tipo="perigo"|tipo=\{[^}]*'perigo'/g
  const USOS_PERMITIDOS = {
    'src/components/ui/dialogo.tsx': 1,
    'src/features/configuracoes/PaginaConfiguracoes.tsx': 1,
  }

  it('tipo="perigo" aparece só nos diálogos de confirmação', () => {
    const usos: Record<string, number> = {}
    for (const nome of readdirSync('src', { recursive: true, encoding: 'utf8' })) {
      if (!nome.endsWith('.tsx') || nome.includes('.test.')) continue
      const arquivo = `src/${nome}`
      const total = (readFileSync(arquivo, 'utf8').match(PERIGO) ?? []).length
      if (total > 0) usos[arquivo] = total
    }
    expect(usos).toEqual(USOS_PERMITIDOS)
  })
})
