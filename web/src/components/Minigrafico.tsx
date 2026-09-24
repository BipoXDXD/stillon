import type { HoraDoSpark } from '@/lib/api/schemas'

// As 24 horas do dashboard em SVG próprio: o Recharts fica fora do
// dashboard (quase metade do bundle — spec do Plano 6, D4). Decorativo: a
// informação está no texto do cartão. Hora sem check é lacuna, não zero.
export function Minigrafico({ horas, agora }: { horas: HoraDoSpark[]; agora: Date }) {
  const largura = 120
  const altura = 28
  const passo = largura / 24
  const inicio = Math.floor(agora.getTime() / 3_600_000) - 24
  const maior = Math.max(1, ...horas.map((h) => h.p50_ms ?? 0))
  return (
    <svg aria-hidden="true" width={largura} height={altura} viewBox={`0 0 ${largura} ${altura}`} className="overflow-visible">
      {horas.map((h) => {
        const i = Math.floor(new Date(h.bucket).getTime() / 3_600_000) - inicio
        if (i < 0 || i >= 24 || h.total === 0) return null
        const fracao = h.ok / h.total
        const alt = Math.max(3, ((h.p50_ms ?? maior) / maior) * altura)
        const cor = fracao >= 0.999 ? 'var(--estado-no-ar)' : fracao >= 0.99 ? 'var(--estado-degradado)' : 'var(--estado-fora)'
        return <rect key={h.bucket} x={i * passo + 0.5} y={altura - alt} width={passo - 1} height={alt} fill={cor} />
      })}
    </svg>
  )
}
