import type { z } from 'zod'
import type { Alvo, Canal, Incidente, ItemDoResumo, Me, PaginaDeStatus, Sessao } from '@/lib/api/schemas'

// Fixtures no formato do contrato: z.input dos schemas das respostas. Uma
// fixture fora do contrato não compila. Os testes de tela (Vitest) e o
// projeto a11y do E2E (e2e/a11y.spec.ts) usam as mesmas: só tipos importados,
// para o Playwright carregar este arquivo sem o alias "@/" em tempo de execução.

export function alvo(p: Partial<z.input<typeof Alvo>> = {}): z.input<typeof Alvo> {
  return {
    id: 'alvo-1',
    name: 'API',
    url: 'https://api.exemplo.com/health',
    method: 'GET',
    interval_sec: 60,
    timeout_ms: 10000,
    expected_status: 200,
    degraded_threshold_ms: 2000,
    enabled: true,
    header_names: [],
    created_at: '2026-09-20T10:00:00Z',
    ...p,
  }
}

export function itemDoResumo(p: Partial<z.input<typeof ItemDoResumo>> = {}): z.input<typeof ItemDoResumo> {
  return {
    id: 'alvo-1',
    name: 'API',
    enabled: true,
    state: 'up',
    last_checked_at: '2026-09-24T11:59:00Z',
    last_latency_ms: 123,
    uptime_24h: 0.99999,
    open_incident_id: null,
    spark: [{ bucket: '2026-09-24T10:00:00Z', total: 60, ok: 60, p50_ms: 110 }],
    ...p,
  }
}

export function incidente(p: Partial<z.input<typeof Incidente>> = {}): z.input<typeof Incidente> {
  return {
    id: 'inc-1',
    target_id: 'alvo-1',
    target_name: 'API',
    started_at: '2026-09-24T11:00:00Z',
    resolved_at: null,
    cause: 'timeout',
    acknowledged_at: null,
    ...p,
  }
}

export function canal(p: Partial<z.input<typeof Canal>> = {}): z.input<typeof Canal> {
  return {
    id: 'canal-1',
    kind: 'discord',
    hint: 'discord.com/…/abcd',
    status: 'verified',
    enabled: true,
    created_at: '2026-09-20T10:00:00Z',
    last_error: null,
    last_failure_at: null,
    ...p,
  }
}

export function sessao(p: Partial<z.input<typeof Sessao>> = {}): z.input<typeof Sessao> {
  return {
    id: 'fam-1',
    created_at: '2026-09-20T10:00:00Z',
    last_used_at: '2026-09-24T11:50:00Z',
    user_agent: 'Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0',
    current: true,
    ...p,
  }
}

export function me(p: Partial<z.input<typeof Me>> = {}): z.input<typeof Me> {
  return { id: 'u1', email: 'dono@exemplo.com', totp_enabled: false, created_at: '2026-09-01T10:00:00Z', ...p }
}

export function pagina(p: Partial<z.input<typeof PaginaDeStatus>> = {}): z.input<typeof PaginaDeStatus> {
  return {
    id: 'pagina-1',
    slug: 'minha',
    title: 'Minha',
    published: true,
    items: [{ target_id: 'alvo-1', display_name: 'API' }],
    public_url: 'http://localhost/p/minha',
    created_at: '2026-09-20T10:00:00Z',
    ...p,
  }
}
