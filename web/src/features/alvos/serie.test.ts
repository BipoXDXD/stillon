import { describe, expect, it } from 'vitest'
import type { Serie } from '@/lib/api/schemas'
import { ehJanela, faixasForaDoAr, latenciasDaSerie, pontosBrutos, pontosHorarios, resumir } from './serie'

const check = (min: number, status: 'up' | 'degraded' | 'down', latency_ms: number) => ({
  checked_at: new Date(Date.UTC(2026, 8, 24, 12, min)).toISOString(),
  status,
  latency_ms,
  status_code: status === 'down' ? null : 200,
  error_kind: status === 'down' ? 'timeout' : null,
})

describe('série bruta', () => {
  it('check fora do ar não entra na linha de latência: vira marca na base', () => {
    const p = pontosBrutos([check(0, 'up', 100), check(1, 'down', 10000), check(2, 'degraded', 2500)])
    expect(p.map((x) => x.latencia)).toEqual([100, null, 2500])
    expect(p.map((x) => x.fora)).toEqual([null, 0, null])
  })

  it('o resumo ignora a latência dos checks fora do ar', () => {
    const s: Serie = {
      resolution: 'raw',
      items: [check(0, 'up', 100), check(1, 'up', 200), check(2, 'down', 10000), check(3, 'up', 300)],
    }
    expect(resumir(s)).toEqual({ mediana: 200, p95: 300, uptime: 0.75, amostras: 4 })
  })

  it('série vazia não inventa número', () => {
    expect(resumir({ resolution: 'raw', items: [] })).toEqual({ mediana: null, p95: null, uptime: null, amostras: 0 })
  })
})

describe('série horária', () => {
  it('usa ok/total e a faixa p50–p95; hora sem sucesso fica sem faixa', () => {
    const itens = [
      { bucket: '2026-09-24T10:00:00Z', total: 60, ok: 60, p50_ms: 100, p95_ms: 300, p99_ms: 400, max_ms: 500 },
      { bucket: '2026-09-24T11:00:00Z', total: 60, ok: 0, p50_ms: null, p95_ms: null, p99_ms: null, max_ms: null },
    ]
    const p = pontosHorarios(itens)
    expect(p[0]).toMatchObject({ p50: 100, faixa: [100, 300], uptime: 1 })
    expect(p[1]).toMatchObject({ p50: null, faixa: null, uptime: 0 })
    expect(resumir({ resolution: '1h', items: itens })).toEqual({ mediana: 100, p95: 300, uptime: 0.5, amostras: 120 })
  })
})

it('ehJanela só aceita as janelas, não o que vem do protótipo', () => {
  expect(ehJanela('7d')).toBe(true)
  expect(ehJanela('toString')).toBe(false)
  expect(ehJanela('__proto__')).toBe(false)
  expect(ehJanela(null)).toBe(false)
})

const t = (min: number) => Date.UTC(2026, 8, 24, 12, min)
const hora = (h: number, total: number, ok: number, p50: number | null = 100, p95: number | null = 300) => ({
  bucket: new Date(Date.UTC(2026, 8, 24, h)).toISOString(),
  total,
  ok,
  p50_ms: p50,
  p95_ms: p95,
  p99_ms: p95,
  max_ms: p95,
})

describe('faixasForaDoAr', () => {
  it('bruta: cada check fora do ar vai até o seguinte, e vizinhos se fundem', () => {
    const s: Serie = { resolution: 'raw', items: [check(0, 'up', 100), check(1, 'down', 10000), check(2, 'down', 10000), check(3, 'up', 100)] }
    expect(faixasForaDoAr(s)).toEqual([{ de: t(1), ate: t(3) }])
  })

  it('bruta: o último check fora do ar vai um passo adiante', () => {
    const s: Serie = { resolution: 'raw', items: [check(0, 'up', 100), check(2, 'up', 100), check(3, 'down', 10000)] }
    expect(faixasForaDoAr(s)).toEqual([{ de: t(3), ate: t(4) }])
  })

  // Review Focus 6: sem dois checks para medir o passo, um minuto.
  it('bruta: um check só, fora do ar, vale um minuto', () => {
    expect(faixasForaDoAr({ resolution: 'raw', items: [check(0, 'down', 10000)] })).toEqual([{ de: t(0), ate: t(1) }])
  })

  it('horária: a hora inteira abaixo de 99% no ar; degradada e vazia não entram', () => {
    const s: Serie = {
      resolution: '1h',
      items: [hora(8, 60, 60), hora(9, 60, 57), hora(10, 60, 0, null, null), hora(11, 1000, 995), hora(12, 0, 0, null, null)],
    }
    expect(faixasForaDoAr(s)).toEqual([{ de: Date.UTC(2026, 8, 24, 9), ate: Date.UTC(2026, 8, 24, 11) }])
  })

  it('série vazia não tem faixa', () => {
    expect(faixasForaDoAr({ resolution: 'raw', items: [] })).toEqual([])
  })
})

describe('latenciasDaSerie', () => {
  it('bruta: só os checks que não caíram', () => {
    const s: Serie = { resolution: 'raw', items: [check(0, 'up', 100), check(1, 'down', 10000), check(2, 'degraded', 2500)] }
    expect(latenciasDaSerie(s)).toEqual([
      { t: t(0), ms: 100 },
      { t: t(2), ms: 2500 },
    ])
  })

  it('horária: o topo da faixa (p95), o p50 sem p95, e nada sem os dois', () => {
    const s: Serie = { resolution: '1h', items: [hora(8, 60, 60, 100, 300), hora(9, 60, 60, 120, null), hora(10, 60, 0, null, null)] }
    expect(latenciasDaSerie(s)).toEqual([
      { t: Date.UTC(2026, 8, 24, 8), ms: 300 },
      { t: Date.UTC(2026, 8, 24, 9), ms: 120 },
    ])
  })
})
