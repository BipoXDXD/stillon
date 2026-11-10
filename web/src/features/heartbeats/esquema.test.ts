import { describe, expect, it } from 'vitest'
import { Heartbeat } from '@/lib/api/schemas'
import { heartbeat } from '@/test/fixtures'
import { comoEditavel, esquemaDoHeartbeat, mensagemDoHeartbeat, paraAlteracao, paraCriacao, valoresDe, valoresPadrao, type ValoresDoHeartbeat } from './esquema'

const RECUSA_DO_CRON = 'Use os cinco campos, com um disparo que exista: 0 3 * * *. Domingo é 0 ou SUN, não 7; de sexta a domingo, 5,6,0.'
const valores = (p: Partial<ValoresDoHeartbeat> = {}): ValoresDoHeartbeat => ({ ...valoresPadrao(), name: 'Backup', ...p })
const hb = (p: Parameters<typeof heartbeat>[0] = {}) => {
  const editavel = comoEditavel(Heartbeat.parse(heartbeat(p)))
  if (editavel === null) throw new Error('fixture com agendamento que o formulário não edita')
  return editavel
}
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
    [365, 'dias', true],
    [31536001, 'segundos', false],
    [366, 'dias', false],
    [1.5, 'minutos', true],
    [60.5, 'segundos', false],
    [Number.NaN, 'minutos', false],
  ] as const)('período de %d %s → %s', (periodoQuantidade, periodoUnidade, valido) => {
    const e = erros(valores({ periodoQuantidade, periodoUnidade }))
    expect(e.periodoQuantidade).toBe(valido ? undefined : 'Entre 1 minuto e 365 dias.')
  })

  it.each([
    [29, 'segundos', false],
    [30, 'segundos', true],
    [365, 'dias', true],
    [31536001, 'segundos', false],
    [Number.NaN, 'minutos', false],
  ] as const)('tolerância de %d %s → %s', (toleranciaQuantidade, toleranciaUnidade, valido) => {
    const e = erros(valores({ toleranciaQuantidade, toleranciaUnidade }))
    expect(e.toleranciaQuantidade).toBe(valido ? undefined : 'Entre 30 segundos e 365 dias.')
  })

  it('cron exige a expressão e o fuso, sem olhar o período', () => {
    const e = erros(valores({ tipo: 'cron', expr: ' ', timezone: '', periodoQuantidade: Number.NaN }))
    expect(e).toEqual({ expr: 'Informe a expressão.', timezone: 'Informe o fuso.' })
  })

  it('cron válido vira o agendamento do contrato', () => {
    const v = valores({ tipo: 'cron', expr: '0 3 * * *', timezone: 'America/Sao_Paulo' })
    expect(paraCriacao(v).schedule).toEqual({ kind: 'cron', expr: '0 3 * * *', timezone: 'America/Sao_Paulo' })
  })

  // Os campos do cron não estão na tela no ramo do período: um erro neles
  // travaria o envio em silêncio. Sabotagem: voltar o .max para a base.
  it('no período, expressão e fuso guardados não seguram o envio', () => {
    expect(erros(valores({ tipo: 'period', expr: 'x'.repeat(101), timezone: 'y'.repeat(65) }))).toEqual({})
  })

  it('no cron, os limites valem', () => {
    expect(erros(valores({ tipo: 'cron', expr: 'x'.repeat(101), timezone: 'y'.repeat(65) }))).toEqual({
      expr: 'No máximo 100 caracteres.',
      timezone: 'No máximo 64 caracteres.',
    })
  })

  // A expressão fica de fora de propósito: quem a julga é o servidor (T11).
  it('não julga a expressão cron', () => {
    expect(erros(valores({ tipo: 'cron', expr: 'isso não é cron', timezone: 'UTC' }))).toEqual({})
  })
})

describe('tags no formulário', () => {
  it('sem tags, o POST não leva o campo', () => {
    expect(paraCriacao(valores())).not.toHaveProperty('tags')
  })

  it('com tags, leva a forma canônica', () => {
    expect(paraCriacao(valores({ tagsTexto: 'Prod, dados, prod' })).tags).toEqual(['dados', 'prod'])
  })

  it.each([
    ['nove tags', 'a,b,c,d,e,f,g,h,i', 'Até 8 tags.'],
    ['longa demais', 'a'.repeat(25), 'Cada tag tem até 24 caracteres.'],
    ['caractere fora da regra', 'a b', 'Use letras, números, hífen e sublinhado, separados por vírgula.'],
  ])('recusa %s', (_, tagsTexto, mensagem) => {
    expect(erros(valores({ tagsTexto }))).toEqual({ tagsTexto: mensagem })
  })

  it('valoresDe preenche o campo, e sem mudança nada vai no PATCH', () => {
    const original = valoresDe(hb({ tags: ['dados', 'prod'] }))
    expect(original.tagsTexto).toBe('dados, prod')
    expect(paraAlteracao(original, { ...original, tagsTexto: 'prod, dados' })).toEqual({})
  })

  // Sabotagem: tratar lista vazia como "sem mudança" → não dá para tirar a última tag.
  it('limpar o campo manda a lista vazia', () => {
    const original = valoresDe(hb({ tags: ['prod'] }))
    expect(paraAlteracao(original, { ...original, tagsTexto: '' })).toEqual({ tags: [] })
  })
})

describe('descrição no formulário', () => {
  // "é" ocupa 2 bytes: contar bytes recusaria os 10000.
  it('o limite é em caracteres, como no servidor: 10000 "é" cabem', () => {
    expect(erros(valores({ description: 'é'.repeat(10000) }))).toEqual({})
    expect(erros(valores({ description: 'é'.repeat(10001) }))).toEqual({ description: 'No máximo 10000 caracteres.' })
  })

  it('emojis também contam um a um', () => {
    expect(erros(valores({ description: '🙂'.repeat(10000) }))).toEqual({})
  })

  it('sem descrição, o POST não leva o campo', () => {
    expect(paraCriacao(esquemaDoHeartbeat.parse(valores({ description: '  ' })))).not.toHaveProperty('description')
  })

  it('com descrição, leva o texto limpo e com as quebras de linha', () => {
    expect(paraCriacao(esquemaDoHeartbeat.parse(valores({ description: ' linha 1\nlinha 2\n' }))).description).toBe('linha 1\nlinha 2')
  })

  it('valoresDe preenche o campo, e sem mudança nada vai no PATCH', () => {
    const original = valoresDe(hb({ description: 'roda no nas' }))
    expect(original.description).toBe('roda no nas')
    expect(paraAlteracao(original, { ...original })).toEqual({})
  })

  // Sabotagem: tratar vazio como "sem mudança" → não dá para apagar a descrição.
  it('limpar o campo manda o texto vazio', () => {
    const original = valoresDe(hb({ description: 'roda no nas' }))
    expect(paraAlteracao(original, { ...original, description: '' })).toEqual({ description: '' })
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

describe('mensagemDoHeartbeat', () => {
  const invalida = (field: string, tela: string) => mensagemDoHeartbeat({ field, reason: 'invalid' }, tela)

  it('conta as linhas a partir de 1', () => {
    expect(invalida('description[0]', 'a')).toBe('Tem um caractere que não pode ser mostrado na linha 1.')
    expect(invalida('description[4]', 'a')).toBe('Tem um caractere que não pode ser mostrado na linha 5.')
  })

  it('soma as linhas em branco do começo, que o envio tira', () => {
    expect(invalida('description[1]', '\n \n\tRoda\nb')).toBe('Tem um caractere que não pode ser mostrado na linha 4.')
  })

  it('espaço no começo da primeira linha não é linha', () => {
    expect(invalida('description[0]', '   Roda')).toBe('Tem um caractere que não pode ser mostrado na linha 1.')
  })

  it('sem linha, a mensagem do campo fica como está', () => {
    expect(invalida('description', '\nRoda')).toBe('Tem um caractere que não pode ser mostrado.')
    expect(mensagemDoHeartbeat({ field: 'description', reason: 'out_of_range' }, '\nRoda')).toBe('No máximo 10000 caracteres.')
  })

  // A recusa do servidor diz as mesmas faixas da tela.
  it.each([
    ['period_sec', 'Entre 1 minuto e 365 dias.'],
    ['grace_sec', 'Entre 30 segundos e 365 dias.'],
  ])('fora da faixa em %s', (field, mensagem) => {
    expect(mensagemDoHeartbeat({ field, reason: 'out_of_range' }, '')).toBe(mensagem)
  })

  it('índice com outro motivo não vira linha de caractere invisível', () => {
    expect(mensagemDoHeartbeat({ field: 'description[1]', reason: 'out_of_range' }, '\nRoda')).toBe('No máximo 10000 caracteres.')
    expect(mensagemDoHeartbeat({ field: 'description[1]', reason: 'required' }, '\nRoda')).toBe('Obrigatório.')
  })

  it('índice só muda a mensagem da descrição', () => {
    expect(invalida('expr', '\nRoda')).toBe(RECUSA_DO_CRON)
    expect(invalida('tags[1]', '\nRoda')).toBe('Use letras, números, hífen e sublinhado, separados por vírgula.')
  })
})
