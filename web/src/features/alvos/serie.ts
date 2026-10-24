import { estadoDaHora } from '@/components/Minigrafico'
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

export type Intervalo = { de: number; ate: number }
export type PontoDeLatencia = { t: number; ms: number }

const HORA_MS = 3_600_000
// Sem dois checks para medir o passo, um check fora do ar vale um minuto.
const PASSO_PADRAO_MS = 60_000

// As faixas hachuradas do gráfico (spec do redesign, Telas > 2). Bruta: cada
// check fora do ar vale até o check seguinte (o último, até um passo
// depois). Horária: a hora inteira abaixo de 99% no ar, o "fora" da barra
// de 24 h do painel. Faixas vizinhas se fundem.
export function faixasForaDoAr(s: Serie): Intervalo[] {
  return fundir(s.resolution === 'raw' ? faixasBrutas(s.items) : faixasHorarias(s.items))
}

function faixasBrutas(itens: Check[]): Intervalo[] {
  const marcas = itens.map((c) => ({ t: Date.parse(c.checked_at), fora: c.status === 'down' }))
  const passo = menorPasso(marcas.map((m) => m.t)) ?? PASSO_PADRAO_MS
  return marcas.flatMap((m, i) => (m.fora ? [{ de: m.t, ate: marcas[i + 1]?.t ?? m.t + passo }] : []))
}

function faixasHorarias(itens: Hora[]): Intervalo[] {
  return itens
    .filter((h) => h.total > 0 && estadoDaHora(h) === 'fora')
    .map((h) => {
      const de = Date.parse(h.bucket)
      return { de, ate: de + HORA_MS }
    })
}

function fundir(faixas: Intervalo[]): Intervalo[] {
  const fundidas: Intervalo[] = []
  for (const f of faixas) {
    const ultima = fundidas.at(-1)
    if (ultima !== undefined && ultima.ate >= f.de) ultima.ate = Math.max(ultima.ate, f.ate)
    else fundidas.push({ ...f })
  }
  return fundidas
}

function menorPasso(tempos: readonly number[]): number | null {
  const passos = tempos.slice(1).map((t, i) => t - (tempos[i] ?? t)).filter((d) => d > 0)
  return passos.length ? Math.min(...passos) : null
}

// Os pontos que a escala Y precisa cobrir: a latência dos checks que não
// caíram (bruta) ou o topo da faixa p50–p95 de cada hora (horária).
export function latenciasDaSerie(s: Serie): PontoDeLatencia[] {
  if (s.resolution === 'raw') {
    return s.items.filter((c) => c.status !== 'down').map((c) => ({ t: Date.parse(c.checked_at), ms: c.latency_ms }))
  }
  return s.items.flatMap((h) => {
    const ms = h.p95_ms ?? h.p50_ms
    return ms === null ? [] : [{ t: Date.parse(h.bucket), ms }]
  })
}
