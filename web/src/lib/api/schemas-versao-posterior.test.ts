import { describe, expect, it } from 'vitest'
import { mensagemDe } from './mensagens'
import { ApiError } from './http'
import { Agendamento, EstadoDoHeartbeat, EventoDeHeartbeat, ListaDeHeartbeats } from './schemas'
import { heartbeat } from '@/test/fixtures'

// O painel tolera o que uma versão posterior do servidor devolve (SP1, piso de
// rollback), sem afrouxar a forma do que já conhece.
describe('Agendamento', () => {
  it('aceita o kind unknown, só com a chave kind', () => {
    expect(Agendamento.parse({ kind: 'unknown' })).toEqual({ kind: 'unknown' })
  })

  it('period sem period_sec continua falhando', () => {
    expect(Agendamento.safeParse({ kind: 'period' }).success).toBe(false)
  })

  it('cron sem fuso continua falhando', () => {
    expect(Agendamento.safeParse({ kind: 'cron', expr: '0 3 * * *' }).success).toBe(false)
  })

  it('kind de uma versão posterior vira unknown, só com a chave kind', () => {
    expect(Agendamento.parse({ kind: 'oncalendar', spec: 'daily' })).toEqual({ kind: 'unknown' })
  })

  it('kind que não é texto continua falhando', () => {
    expect(Agendamento.safeParse({ kind: 3 }).success).toBe(false)
  })

  it('a lista com um agendamento de versão posterior não derruba o parse', () => {
    const lista = ListaDeHeartbeats.parse({
      items: [heartbeat({ id: 'hb-1' }), heartbeat({ id: 'hb-2', schedule: { kind: 'oncalendar', spec: 'daily' } })],
    })
    expect(lista.items.map((h) => h.schedule)).toEqual([{ kind: 'period', period_sec: 86400 }, { kind: 'unknown' }])
  })
})

describe('EventoDeHeartbeat.kind', () => {
  const evento = { id: 'ev-1', at: '2026-09-24T11:58:00Z', runtime_ms: null, exit_code: null }

  it.each(['start', 'success', 'fail'])('%s fica como veio', (kind) => {
    expect(EventoDeHeartbeat.parse({ ...evento, kind }).kind).toBe(kind)
  })

  it('tipo desconhecido vira unknown', () => {
    expect(EventoDeHeartbeat.parse({ ...evento, kind: 'log' }).kind).toBe('unknown')
  })

  it('kind que não é texto continua falhando', () => {
    expect(EventoDeHeartbeat.safeParse({ ...evento, kind: 7 }).success).toBe(false)
  })
})

describe('EstadoDoHeartbeat', () => {
  it.each(['waiting', 'up', 'late', 'failing', 'paused'])('%s fica como veio', (estado) => {
    expect(EstadoDoHeartbeat.parse(estado)).toBe(estado)
  })

  it('estado desconhecido vira unknown', () => {
    expect(EstadoDoHeartbeat.parse('grace')).toBe('unknown')
  })

  it('estado que não é texto continua falhando', () => {
    expect(EstadoDoHeartbeat.safeParse(null).success).toBe(false)
  })
})

describe('mensagem de schedule_unsupported', () => {
  it('diz que o agendamento não pode ser editado nesta versão', () => {
    expect(mensagemDe(new ApiError(409, 'schedule_unsupported', ''))).toBe('Este agendamento não pode ser editado nesta versão.')
  })
})
