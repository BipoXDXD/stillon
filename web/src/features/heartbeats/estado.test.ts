import { describe, expect, it } from 'vitest'
import type { EstadoDoHeartbeat } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'
import { estadoDoHeartbeat, partirDuracao, textoDaDuracao, textoDoAgendamento, textoDoHeartbeat } from './estado'

describe('estado do heartbeat na tela', () => {
  it.each([
    ['up', 'up', 'Em dia'],
    ['late', 'down', 'Atrasado'],
    ['failing', 'down', 'Falhando'],
    ['waiting', 'unknown', 'Aguardando'],
    ['paused', 'pausado', 'Pausado'],
    ['unknown', 'unknown', 'Estado desconhecido'],
  ] satisfies [EstadoDoHeartbeat, EstadoExibido, string][])('%s tem a gravidade de %s e se chama %s', (estado, exibido, texto) => {
    expect(estadoDoHeartbeat(estado)).toBe(exibido)
    expect(textoDoHeartbeat(estado)).toBe(texto)
  })
})

describe('partirDuracao', () => {
  it.each([
    [1, 1, 'segundos'],
    [59, 59, 'segundos'],
    [60, 1, 'minutos'],
    [5400, 90, 'minutos'],
    [3600, 1, 'horas'],
    [86400, 1, 'dias'],
    [90000, 25, 'horas'],
  ])('%i s → %i %s', (s, quantidade, unidade) => {
    expect(partirDuracao(s)).toEqual({ quantidade, unidade })
  })
})

describe('textoDaDuracao', () => {
  it.each([
    [30, '30 s'],
    [60, '1 min'],
    [90, '90 s'],
    [300, '5 min'],
    [3600, '1 h'],
    [5400, '90 min'],
    [86400, '1 dia'],
    [172800, '2 dias'],
    [2678400, '31 dias'],
  ])('%i s → %s', (s, texto) => {
    expect(textoDaDuracao(s)).toBe(texto)
  })
})

describe('textoDoAgendamento', () => {
  it('período', () => {
    expect(textoDoAgendamento({ kind: 'period', period_sec: 86400 })).toBe('a cada 1 dia')
  })

  it('cron, com o fuso escrito', () => {
    expect(textoDoAgendamento({ kind: 'cron', expr: '0 3 * * *', timezone: 'America/Sao_Paulo' })).toBe('cron 0 3 * * *, America/Sao_Paulo')
  })

  it('agendamento de versão posterior diz que não é suportado', () => {
    expect(textoDoAgendamento({ kind: 'unknown' })).toBe('Agendamento não suportado nesta versão')
  })
})
