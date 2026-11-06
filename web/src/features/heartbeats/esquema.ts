import { z } from 'zod'
import type { AlteracaoHeartbeat, EntradaHeartbeat } from '@/lib/api/cliente'
import type { ErroDeCampo } from '@/lib/api/http'
import { mensagemDaLinhaDaDescricao, mensagemDoCampo } from '@/lib/api/mensagens'
import type { Agendamento, Heartbeat } from '@/lib/api/schemas'
import { fuso, pontosDeCodigo } from '@/lib/format'
import { erroNasTags, separarTags } from '@/lib/tags'
import { partirDuracao, SEGUNDOS_DA_UNIDADE, type Unidade } from './estado'

// O formulário do heartbeat, com os limites da §7 da spec. A expressão cron
// quem julga é o servidor: um segundo parser aqui é o risco que o D17 evitou
// (spec da tela, T11).
const UNIDADES = ['dias', 'horas', 'minutos', 'segundos'] as const

const LIMITES = {
  periodo: { min: 60, max: 2_678_400, texto: 'Entre 1 minuto e 31 dias.' },
  tolerancia: { min: 30, max: 604_800, texto: 'Entre 30 segundos e 7 dias.' },
}
const MAX_NOME = 100
const MAX_DESCRICAO = 1000
const MAX_EXPR = 100
const MAX_FUSO = 64
const GRACE_PADRAO_SEC = 300
const PERIODO_PADRAO_SEC = 86_400

export const esquemaDoHeartbeat = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, 'Informe um nome.')
      .refine((s) => pontosDeCodigo(s) <= MAX_NOME, `No máximo ${MAX_NOME} caracteres.`),
    description: z
      .string()
      .trim()
      .refine((s) => pontosDeCodigo(s) <= MAX_DESCRICAO, `No máximo ${MAX_DESCRICAO} caracteres.`),
    tagsTexto: z.string(),
    tipo: z.enum(['period', 'cron']),
    periodoQuantidade: z.number().or(z.nan()),
    periodoUnidade: z.enum(UNIDADES),
    // Os limites destes dois moram no ramo cron: no de período os campos não
    // estão na tela, e um erro neles travaria o envio em silêncio.
    expr: z.string().trim(),
    timezone: z.string().trim(),
    toleranciaQuantidade: z.number().or(z.nan()),
    toleranciaUnidade: z.enum(UNIDADES),
  })
  .superRefine((v, ctx) => {
    const fora = (campo: string, mensagem: string) => ctx.addIssue({ code: 'custom', path: [campo], message: mensagem })
    const erroDasTags = erroNasTags(v.tagsTexto)
    if (erroDasTags) fora('tagsTexto', erroDasTags)
    if (!dentroDe(emSegundos(v.toleranciaQuantidade, v.toleranciaUnidade), LIMITES.tolerancia)) {
      fora('toleranciaQuantidade', LIMITES.tolerancia.texto)
    }
    if (v.tipo === 'period') {
      if (!dentroDe(emSegundos(v.periodoQuantidade, v.periodoUnidade), LIMITES.periodo)) fora('periodoQuantidade', LIMITES.periodo.texto)
      return
    }
    if (v.expr === '') fora('expr', 'Informe a expressão.')
    else if (v.expr.length > MAX_EXPR) fora('expr', `No máximo ${MAX_EXPR} caracteres.`)
    if (v.timezone === '') fora('timezone', 'Informe o fuso.')
    else if (v.timezone.length > MAX_FUSO) fora('timezone', `No máximo ${MAX_FUSO} caracteres.`)
  })

export type ValoresDoHeartbeat = z.infer<typeof esquemaDoHeartbeat>

function emSegundos(quantidade: number, unidade: Unidade): number {
  return quantidade * SEGUNDOS_DA_UNIDADE[unidade]
}

function dentroDe(segundos: number, { min, max }: { min: number; max: number }): boolean {
  return Number.isInteger(segundos) && segundos >= min && segundos <= max
}

export function valoresPadrao(): ValoresDoHeartbeat {
  const periodo = partirDuracao(PERIODO_PADRAO_SEC)
  const tolerancia = partirDuracao(GRACE_PADRAO_SEC)
  return {
    name: '',
    description: '',
    tagsTexto: '',
    tipo: 'period',
    periodoQuantidade: periodo.quantidade,
    periodoUnidade: periodo.unidade,
    expr: '',
    timezone: fuso(),
    toleranciaQuantidade: tolerancia.quantidade,
    toleranciaUnidade: tolerancia.unidade,
  }
}

export function valoresDe(hb: Heartbeat): ValoresDoHeartbeat {
  const tolerancia = partirDuracao(hb.grace_sec)
  const base = { ...valoresPadrao(), name: hb.name, description: hb.description, tagsTexto: hb.tags.join(', '), toleranciaQuantidade: tolerancia.quantidade, toleranciaUnidade: tolerancia.unidade }
  if (hb.schedule.kind === 'cron') return { ...base, tipo: 'cron', expr: hb.schedule.expr, timezone: hb.schedule.timezone }
  const periodo = partirDuracao(hb.schedule.period_sec)
  return { ...base, tipo: 'period', periodoQuantidade: periodo.quantidade, periodoUnidade: periodo.unidade }
}

function agendamento(v: ValoresDoHeartbeat): Agendamento {
  return v.tipo === 'cron'
    ? { kind: 'cron', expr: v.expr, timezone: v.timezone }
    : { kind: 'period', period_sec: emSegundos(v.periodoQuantidade, v.periodoUnidade) }
}

const graceDe = (v: ValoresDoHeartbeat) => emSegundos(v.toleranciaQuantidade, v.toleranciaUnidade)

export function paraCriacao(v: ValoresDoHeartbeat): EntradaHeartbeat {
  const tags = separarTags(v.tagsTexto)
  return {
    name: v.name,
    schedule: agendamento(v),
    grace_sec: graceDe(v),
    ...(tags.length > 0 && { tags }),
    ...(v.description !== '' && { description: v.description }),
  }
}

// Só o que mudou de valor (spec da tela, T12): "1 dia" e "24 horas" são o mesmo
// período, e reenviá-lo não é mudança.
export function paraAlteracao(original: ValoresDoHeartbeat, v: ValoresDoHeartbeat): AlteracaoHeartbeat {
  const a: AlteracaoHeartbeat = {}
  if (v.name !== original.name) a.name = v.name
  if (v.description !== original.description) a.description = v.description
  const tags = separarTags(v.tagsTexto)
  if (tags.join() !== separarTags(original.tagsTexto).join()) a.tags = tags
  if (JSON.stringify(agendamento(v)) !== JSON.stringify(agendamento(original))) a.schedule = agendamento(v)
  if (graceDe(v) !== graceDe(original)) a.grace_sec = graceDe(v)
  return a
}

// O servidor conta as linhas do texto enviado, e o envio tira as linhas em
// branco do começo: a contagem que o dono entende é a da tela.
export function mensagemDoHeartbeat({ field, reason }: ErroDeCampo, descricaoNaTela: string): string {
  const indice = /^description\[(\d+)\]$/.exec(field)?.[1]
  if (indice === undefined) return mensagemDoCampo(field, reason)
  const recuo = descricaoNaTela.slice(0, descricaoNaTela.length - descricaoNaTela.trimStart().length)
  return mensagemDaLinhaDaDescricao(Number(indice) + recuo.split('\n').length)
}

// O campo do formulário que recebe o erro de cada campo do servidor.
export const campoDoServidor: Partial<Record<string, keyof ValoresDoHeartbeat>> = {
  name: 'name',
  description: 'description',
  tags: 'tagsTexto',
  period_sec: 'periodoQuantidade',
  expr: 'expr',
  timezone: 'timezone',
  grace_sec: 'toleranciaQuantidade',
}
