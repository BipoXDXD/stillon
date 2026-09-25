import type { Check, Hora, Serie } from '@/lib/api/schemas'

// A série do detalhe do alvo em forma de desenho e de texto. Funções puras:
// o Recharts não desenha no jsdom, então o que importa (o que é "fora", o
// resumo em texto) é testado aqui, e o desenho, no E2E.

// As chaves são os valores do ?window= do servidor (B6 do Plano 10), que
// calcula o from; a duração fica como documentação do nome.
export const JANELAS = {
  '1h': 3_600_000,
  '6h': 6 * 3_600_000,
  '24h': 24 * 3_600_000,
  '7d': 7 * 86_400_000,
  '30d': 30 * 86_400_000,
  '90d': 90 * 86_400_000,
} as const

export type Janela = keyof typeof JANELAS

export function ehJanela(v: string | null): v is Janela {
  // hasOwn, e não `in`: "toString" está no protótipo de todo objeto.
  return v !== null && Object.hasOwn(JANELAS, v)
}

export type PontoBruto = { t: number; latencia: number | null; fora: number | null }
export type PontoHorario = { t: number; p50: number | null; faixa: [number, number] | null; uptime: number | null }

// A latência de um check "down" é o tempo até o timeout ou a recusa, não a
// do serviço (D23 do Plano 5): ela não entra na linha; o check vira uma
// marca na base do gráfico.
export function pontosBrutos(itens: Check[]): PontoBruto[] {
  return itens.map((c) => ({
    t: new Date(c.checked_at).getTime(),
    latencia: c.status === 'down' ? null : c.latency_ms,
    fora: c.status === 'down' ? 0 : null,
  }))
}

export function pontosHorarios(itens: Hora[]): PontoHorario[] {
  return itens.map((h) => ({
    t: new Date(h.bucket).getTime(),
    p50: h.p50_ms,
    faixa: h.p50_ms !== null && h.p95_ms !== null ? [h.p50_ms, h.p95_ms] : null,
    uptime: h.total ? h.ok / h.total : null,
  }))
}

function percentil(ordenados: number[], p: number): number | null {
  if (!ordenados.length) return null
  const i = Math.max(0, Math.ceil(p * ordenados.length) - 1)
  return ordenados[i] ?? null
}

export type ResumoDaSerie = { mediana: number | null; p95: number | null; uptime: number | null; amostras: number }

export function resumir(s: Serie): ResumoDaSerie {
  if (s.resolution === 'raw') {
    const bons = s.items.filter((c) => c.status !== 'down').map((c) => c.latency_ms).sort((a, b) => a - b)
    return {
      mediana: percentil(bons, 0.5),
      p95: percentil(bons, 0.95),
      uptime: s.items.length ? bons.length / s.items.length : null,
      amostras: s.items.length,
    }
  }
  const medianas = s.items.map((h) => h.p50_ms).filter((v): v is number => v !== null).sort((a, b) => a - b)
  const p95s = s.items.map((h) => h.p95_ms).filter((v): v is number => v !== null)
  const total = s.items.reduce((n, h) => n + h.total, 0)
  const ok = s.items.reduce((n, h) => n + h.ok, 0)
  return {
    mediana: percentil(medianas, 0.5),
    p95: p95s.length ? Math.max(...p95s) : null,
    uptime: total ? ok / total : null,
    amostras: total,
  }
}
