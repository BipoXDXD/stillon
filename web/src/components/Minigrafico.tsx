import type { HoraDoSpark } from '@/lib/api/schemas'
import { plural } from '@/lib/format'

export type EstadoDaHora = 'no-ar' | 'degradado' | 'fora'

// Forma + cor (spec do redesign, Telas > 1): a hora boa é uma barra cheia e
// cinza; a degradada tem dois terços da altura, e a fora do ar, um terço,
// nas cores do estado. A altura carrega o sinal sem depender da cor (WCAG
// 1.4.1). O cinza é a borda de controle, com 3:1 sobre o fundo e o cartão
// (src/tokens.test.ts): um cinza mais claro se confundiria com a hora sem
// check, que é lacuna. As alturas são as da página pública (PR 7); lá, o dia sem dados é contorno.
const ESTILO: Record<EstadoDaHora, { cor: string; fracao: number }> = {
  'no-ar': { cor: 'var(--borda-controle)', fracao: 1 },
  degradado: { cor: 'var(--estado-degradado)', fracao: 2 / 3 },
  fora: { cor: 'var(--estado-fora)', fracao: 1 / 3 },
}

const HORAS = 24
const PASSO = 4
const LARGURA = HORAS * PASSO
const ALTURA = 24
const MS_POR_HORA = 3_600_000

// As 24 horas do painel em SVG próprio: o Recharts fica fora do painel
// (quase metade do bundle, spec do Plano 6, D4). Hora sem check é lacuna,
// não zero. O nome acessível resume as horas por estado.
export function Minigrafico({ horas, agora }: { horas: HoraDoSpark[]; agora: Date }) {
  const inicio = Math.floor(agora.getTime() / MS_POR_HORA) - HORAS
  const desenhadas = horas.flatMap((h) => {
    const i = Math.floor(new Date(h.bucket).getTime() / MS_POR_HORA) - inicio
    return i < 0 || i >= HORAS || h.total === 0 ? [] : [{ h, x: i * PASSO, estado: estadoDaHora(h) }]
  })
  return (
    // SVG desenhado aqui não vira <img>: role="img" com aria-label é o
    // padrão ARIA para dar nome a um SVG inline.
    // oxlint-disable-next-line jsx-a11y/prefer-tag-over-role
    <svg role="img"
      aria-label={`Últimas 24 h: ${resumir(desenhadas.map((d) => d.estado))}`}
      width={LARGURA}
      height={ALTURA}
      viewBox={`0 0 ${LARGURA} ${ALTURA}`}
    >
      {desenhadas.map(({ h, x, estado }) => {
        const { cor, fracao } = ESTILO[estado]
        const altura = ALTURA * fracao
        return <rect key={h.bucket} x={x} y={ALTURA - altura} width={PASSO - 1} height={altura} fill={cor} />
      })}
    </svg>
  )
}

// Os limiares de sempre (99,9% e 99%, inclusivos). O gráfico do detalhe usa
// o mesmo "fora" para hachurar a hora (src/features/alvos/serie.ts).
export function estadoDaHora(h: { total: number; ok: number }): EstadoDaHora {
  const fracao = h.ok / h.total
  if (fracao >= 0.999) return 'no-ar'
  return fracao >= 0.99 ? 'degradado' : 'fora'
}

const NOMES: Record<EstadoDaHora, [string, string]> = {
  'no-ar': ['hora no ar', 'horas no ar'],
  degradado: ['hora degradada', 'horas degradadas'],
  fora: ['hora fora do ar', 'horas fora do ar'],
}

function resumir(estados: EstadoDaHora[]): string {
  if (estados.length === 0) return 'sem checks'
  return (Object.keys(NOMES) as EstadoDaHora[])
    .map((e) => [e, estados.filter((x) => x === e).length] as const)
    .filter(([, n]) => n > 0)
    .map(([e, n]) => plural(n, ...NOMES[e]))
    .join(', ')
}
