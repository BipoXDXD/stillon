import type { HoraDoSpark } from '@/lib/api/schemas'

type EstadoDaHora = 'no-ar' | 'degradado' | 'fora'

// Cor da barra e altura da marca sob ela. A marca é o sinal que não depende
// da cor (WCAG 1.4.1): verde e vermelho têm 1,09:1 entre si e são o par que
// a deuteranopia não separa. Hora no ar não tem marca.
const ESTILO: Record<EstadoDaHora, { cor: string; marca: number }> = {
  'no-ar': { cor: 'var(--estado-no-ar)', marca: 0 },
  degradado: { cor: 'var(--estado-degradado)', marca: 2 },
  fora: { cor: 'var(--estado-fora)', marca: 4 },
}

const LARGURA = 120
const ALTURA_DAS_BARRAS = 28
const VAO = 2
const MARCA_MAXIMA = 4
const ALTURA = ALTURA_DAS_BARRAS + VAO + MARCA_MAXIMA
const PASSO = LARGURA / 24

// As 24 horas do dashboard em SVG próprio: o Recharts fica fora do
// dashboard (quase metade do bundle — spec do Plano 6, D4). Altura da barra
// = latência mediana; cor e marca = uptime da hora. Hora sem check é
// lacuna, não zero. O nome acessível resume as horas por estado.
export function Minigrafico({ horas, agora }: { horas: HoraDoSpark[]; agora: Date }) {
  const inicio = Math.floor(agora.getTime() / 3_600_000) - 24
  const maior = Math.max(1, ...horas.map((h) => h.p50_ms ?? 0))
  const desenhadas = horas.flatMap((h) => {
    const i = Math.floor(new Date(h.bucket).getTime() / 3_600_000) - inicio
    return i < 0 || i >= 24 || h.total === 0 ? [] : [{ h, x: i * PASSO + 0.5, estado: estadoDaHora(h) }]
  })
  return (
    <svg
      role="img"
      aria-label={`Últimas 24 h: ${resumir(desenhadas.map((d) => d.estado))}`}
      width={LARGURA}
      height={ALTURA}
      viewBox={`0 0 ${LARGURA} ${ALTURA}`}
      className="overflow-visible"
    >
      {desenhadas.map(({ h, x, estado }) => {
        const alt = Math.max(3, ((h.p50_ms ?? maior) / maior) * ALTURA_DAS_BARRAS)
        const { cor, marca } = ESTILO[estado]
        return (
          <g key={h.bucket}>
            <rect x={x} y={ALTURA_DAS_BARRAS - alt} width={PASSO - 1} height={alt} fill={cor} />
            {marca > 0 && <path d={`M${x} ${ALTURA - marca}h${PASSO - 1}v${marca}h-${PASSO - 1}z`} fill={cor} />}
          </g>
        )
      })}
    </svg>
  )
}

function estadoDaHora(h: HoraDoSpark): EstadoDaHora {
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
    .map(([e, n]) => `${n} ${NOMES[e][n === 1 ? 0 : 1]}`)
    .join(', ')
}
