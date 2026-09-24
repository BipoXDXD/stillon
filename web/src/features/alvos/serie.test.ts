import { describe, expect, it } from 'vitest'
import type { Serie } from '@/lib/api/schemas'
import { desde, pontosBrutos, pontosHorarios, resumir } from './serie'

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

it('desde() sai em RFC 3339 sem milissegundos', () => {
  expect(desde('24h', Date.UTC(2026, 8, 24, 12, 0, 0, 123))).toBe('2026-09-23T12:00:00Z')
})
