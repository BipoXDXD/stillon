import type { Page, Route } from '@playwright/test'
import type { z } from 'zod'
import type { ConfirmacaoTotp, InicioDaTroca, Serie } from '../src/lib/api/schemas'
import { alvo, canal, eventoDeHeartbeat, heartbeat, incidente, incidenteDeHeartbeat, itemDoResumo, me, pagina, sessao } from '../src/test/fixtures'

// A API do painel respondida pelo próprio teste (page.route): o a11y.spec.ts
// e o roteiro de captura (capturas.spec.ts) veem os mesmos dados. As
// respostas vêm das MESMAS fixtures tipadas dos testes de tela
// (src/test/fixtures.ts, z.input dos schemas): uma resposta fora do contrato
// não compila aqui também (spec do Plano 6 §7.4).
export const agora = new Date().toISOString()
export const hora = new Date(Math.floor(Date.now() / 3_600_000) * 3_600_000 - 3_600_000).toISOString()
const spark = [{ bucket: hora, total: 60, ok: 60, p50_ms: 110 }]
export const item = (id: string, name: string, state: 'up' | 'degraded' | 'down' | 'unknown', enabled = true) =>
  itemDoResumo({
    id,
    name,
    enabled,
    state,
    last_checked_at: agora,
    last_latency_ms: state === 'down' ? null : 120,
    uptime_24h: 0.9993,
    open_incident_id: state === 'down' ? 'inc-1' : null,
    spark,
  })
const a1 = alvo({ id: 'a1', header_names: ['Authorization'], created_at: agora })
const bruta: z.input<typeof Serie> = {
  resolution: 'raw',
  items: [
    { checked_at: agora, status: 'up', latency_ms: 120, status_code: 200, error_kind: null },
    { checked_at: agora, status: 'down', latency_ms: 10000, status_code: null, error_kind: 'timeout' },
  ],
}
// A janela de 7 dias vem por hora (o corte de 48 h é do servidor): as barras
// de uptime da hora aparecem (spec do Plano 6 §4.4).
const horaria: z.input<typeof Serie> = {
  resolution: '1h',
  items: [
    { bucket: hora, total: 60, ok: 57, p50_ms: 110, p95_ms: 300, p99_ms: 400, max_ms: 500 },
    { bucket: agora, total: 30, ok: 30, p50_ms: 100, p95_ms: 200, p99_ms: 250, max_ms: 300 },
  ],
}
const heartbeats = [
  heartbeat({ id: 'h1', name: 'Backup noturno', state: 'late', open_incident_id: 'hi1', deadline_at: agora, last_ping_at: agora, last_success_at: agora, created_at: agora }),
  heartbeat({ id: 'h2', name: 'Relatório', last_ping_at: agora, last_success_at: agora, deadline_at: agora, created_at: agora }),
]
const respostas: Record<string, unknown> = {
  'POST /auth/refresh': { access_token: 'token-de-teste', expires_in: 900 },
  'GET /me': me({ created_at: agora }),
  'GET /targets/summary': {
    items: [
      { ...item('a1', 'API', 'up'), tags: ['prod', 'api'] }, { ...item('a2', 'Site', 'degraded'), tags: ['prod'] },
      { ...item('a3', 'Banco', 'down'), tags: ['prod', 'dados', 'backend', 'cliente-a'] },
      item('a4', 'Novo', 'unknown'), item('a5', 'Parado', 'unknown', false),
    ],
  },
  'GET /targets': { items: [a1] },
  'GET /targets/a1': a1,
  'GET /incidents': {
    items: [incidente({ id: 'inc-1', target_id: 'a3', target_name: 'Banco', started_at: agora, cause: 'conn_refused' })],
  },
  'GET /heartbeats': { items: heartbeats },
  'GET /heartbeats/h1': heartbeats[0],
  'GET /heartbeats/h1/events': {
    items: [
      eventoDeHeartbeat({ id: 'e1', at: agora, kind: 'fail' }),
      eventoDeHeartbeat({ id: 'e2', at: agora, kind: 'success', runtime_ms: 183_000 }),
      eventoDeHeartbeat({ id: 'e3', at: agora, kind: 'start' }),
    ],
  },
  'GET /heartbeats/h1/incidents': {
    items: [
      incidenteDeHeartbeat({ id: 'hi1', heartbeat_id: 'h1', heartbeat_name: 'Backup noturno', started_at: agora }),
      incidenteDeHeartbeat({ id: 'hi2', heartbeat_id: 'h1', heartbeat_name: 'Backup noturno', started_at: agora, resolved_at: agora, cause: 'heartbeat_failed' }),
    ],
  },
  'GET /channels': {
    items: [
      canal({ id: 'c1', created_at: agora }),
      canal({ id: 'c2', hint: 'discord.com/…/zzzz', status: 'failing', created_at: agora, last_error: 'invalid_channel', last_failure_at: agora }),
      canal({ id: 'c3', kind: 'email', hint: 'd***@exemplo.com', status: 'pending', created_at: agora }),
      // Pausado (spec do Plano 9 §4.1): a linha "Pausado" e o "Retomar".
      canal({ id: 'c4', hint: 'discord.com/…/pppp', enabled: false, created_at: agora }),
    ],
  },
  'GET /status-pages': { items: [pagina({ id: 'p1', items: [{ target_id: 'a1', display_name: 'API' }], created_at: agora })] },
  'GET /sessions': { items: [sessao({ id: 'f1', created_at: agora, last_used_at: agora })] },
}

// A troca de autenticador (spec do Plano 9 §4.2): as respostas do rotate e
// do rotate/confirm, no formato do enroll (mais o rotation, A7 do Plano 10)
// e do confirm.
export const SEGREDO_NOVO = 'KRSXG5CTMVRXEZLU'
const troca: z.input<typeof InicioDaTroca> = {
  otpauth_uri: `otpauth://totp/StillOn:dono%40exemplo.com?secret=${SEGREDO_NOVO}&issuer=StillOn`,
  rotation: '0123456789abcdef0123456789abcdef',
}
export const codigosNovos: z.input<typeof ConfirmacaoTotp> = {
  recovery_codes: Array.from({ length: 10 }, (_, i) => `NOVO${String(i).padStart(2, '0')}CODIGO000${i}`),
}
respostas['POST /heartbeats'] = { ...heartbeats[0], id: 'h1', ping_url: `https://stillon.exemplo/ping/${'t'.repeat(43)}` }
respostas['POST /auth/totp/rotate'] = troca
respostas['POST /auth/totp/rotate/confirm'] = codigosNovos

function respostaDe(metodo: string, u: URL): unknown {
  const chave = `${metodo} ${u.pathname.replace('/api/v1', '')}`
  // A janela vem pelo nome (?window=, B6 do Plano 10); acima de 48 h, o
  // servidor responde por hora.
  if (chave === 'GET /targets/a1/checks') {
    return ['7d', '30d', '90d'].includes(u.searchParams.get('window') ?? '') ? horaria : bruta
  }
  return respostas[chave]
}

export async function imitarApi(page: Page) {
  await page.route('**/api/v1/**', (route: Route) => {
    const corpo = respostaDe(route.request().method(), new URL(route.request().url()))
    return corpo === undefined
      ? route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '' }, trace_id: 't' } })
      : route.fulfill({ status: 200, json: corpo })
  })
}
