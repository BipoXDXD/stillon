import { describe, expect, it } from 'vitest'
import { Heartbeat } from '@/lib/api/schemas'
import { heartbeat } from '@/test/fixtures'
import { esquemaDoHeartbeat, paraAlteracao, paraCriacao, valoresDe, valoresPadrao, type ValoresDoHeartbeat } from './esquema'

const valores = (p: Partial<ValoresDoHeartbeat> = {}): ValoresDoHeartbeat => ({ ...valoresPadrao(), name: 'Backup', ...p })
const hb = (p: Parameters<typeof heartbeat>[0] = {}) => Heartbeat.parse(heartbeat(p))
const erros = (v: ValoresDoHeartbeat) => {
  const r = esquemaDoHeartbeat.safeParse(v)
  return r.success ? {} : Object.fromEntries(r.error.issues.map((i) => [i.path.join('.'), i.message]))
}

describe('validação do formulário', () => {
  it('o padrão, com nome, passa: 1 dia de período e 5 minutos de tolerância', () => {
    expect(erros(valores())).toEqual({})
    expect(paraCriacao(valores())).toEqual({ name: 'Backup', schedule: { kind: 'period', period_sec: 86400 }, grace_sec: 300 })
  })

  it.each([
    ['vazio', '   ', 'Informe um nome.'],
    ['acima do limite', 'a'.repeat(101), 'No máximo 100 caracteres.'],
  ])('nome %s', (_, name, mensagem) => {
    expect(erros(valores({ name })).name).toBe(mensagem)
  })

  it('o limite do nome é em caracteres, como no servidor: 100 emojis cabem', () => {
    expect(erros(valores({ name: '😀'.repeat(100) }))).toEqual({})
  })

  // Sabotagem: trocar >= por > ou <= por < em dentroDe.
  it.each([
    [59, 'segundos', false],
    [60, 'segundos', true],
    [1, 'minutos', true],
    [31, 'dias', true],
    [2678401, 'segundos', false],
    [32, 'dias', false],
    [1.5, 'minutos', true],
    [60.5, 'segundos', false],
    [Number.NaN, 'minutos', false],
  ] as const)('período de %d %s → %s', (periodoQuantidade, periodoUnidade, valido) => {
    const e = erros(valores({ periodoQuantidade, periodoUnidade }))
    expect(e.periodoQuantidade).toBe(valido ? undefined : 'Entre 1 minuto e 31 dias.')
  })

  it.each([
    [29, 'segundos', false],
    [30, 'segundos', true],
    [7, 'dias', true],
    [604801, 'segundos', false],
    [Number.NaN, 'minutos', false],
  ] as const)('tolerância de %d %s → %s', (toleranciaQuantidade, toleranciaUnidade, valido) => {
    const e = erros(valores({ toleranciaQuantidade, toleranciaUnidade }))
    expect(e.toleranciaQuantidade).toBe(valido ? undefined : 'Entre 30 segundos e 7 dias.')
  })

  it('cron exige a expressão e o fuso, sem olhar o período', () => {
    const e = erros(valores({ tipo: 'cron', expr: ' ', timezone: '', periodoQuantidade: Number.NaN }))
    expect(e).toEqual({ expr: 'Informe a expressão.', timezone: 'Informe o fuso.' })
  })

  it('cron válido vira o agendamento do contrato', () => {
    const v = valores({ tipo: 'cron', expr: '0 3 * * *', timezone: 'America/Sao_Paulo' })
    expect(paraCriacao(v).schedule).toEqual({ kind: 'cron', expr: '0 3 * * *', timezone: 'America/Sao_Paulo' })
  })

  // A expressão fica de fora de propósito: quem a julga é o servidor (T11).
  it('não julga a expressão cron', () => {
    expect(erros(valores({ tipo: 'cron', expr: 'isso não é cron', timezone: 'UTC' }))).toEqual({})
  })
})

describe('valoresDe', () => {
  it('período: a maior unidade exata', () => {
    expect(valoresDe(hb({ schedule: { kind: 'period', period_sec: 5400 }, grace_sec: 3600 }))).toMatchObject({
      tipo: 'period',
      periodoQuantidade: 90,
      periodoUnidade: 'minutos',
      toleranciaQuantidade: 1,
      toleranciaUnidade: 'horas',
    })
  })

  it('cron: a expressão e o fuso guardados', () => {
    expect(valoresDe(hb({ schedule: { kind: 'cron', expr: '0 3 * * *', timezone: 'UTC' } }))).toMatchObject({
      tipo: 'cron',
      expr: '0 3 * * *',
      timezone: 'UTC',
    })
  })
})

describe('paraAlteracao', () => {
  const base = valoresDe(hb({ name: 'Backup', schedule: { kind: 'period', period_sec: 86400 }, grace_sec: 300 }))

  // Sabotagem: mandar o schedule sempre.
  it('sem mudança, um PATCH vazio', () => {
    expect(paraAlteracao(base, base)).toEqual({})
  })

  it('o mesmo período em outra unidade não é mudança', () => {
    expect(paraAlteracao(base, { ...base, periodoQuantidade: 24, periodoUnidade: 'horas' })).toEqual({})
  })

  it('só o que mudou', () => {
    expect(paraAlteracao(base, { ...base, name: 'Outro' })).toEqual({ name: 'Outro' })
    expect(paraAlteracao(base, { ...base, toleranciaQuantidade: 10 })).toEqual({ grace_sec: 600 })
    expect(paraAlteracao(base, { ...base, periodoQuantidade: 2 })).toEqual({ schedule: { kind: 'period', period_sec: 172800 } })
  })

  it('trocar para cron manda o agendamento inteiro', () => {
    expect(paraAlteracao(base, { ...base, tipo: 'cron', expr: '0 3 * * *', timezone: 'UTC' })).toEqual({
      schedule: { kind: 'cron', expr: '0 3 * * *', timezone: 'UTC' },
    })
  })
})
