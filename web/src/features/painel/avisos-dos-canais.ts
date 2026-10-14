import type { Canal } from '@/lib/api/schemas'
import { plural } from '@/lib/format'

type ConsultaDosCanais = { data: { items: readonly Canal[] } | undefined; error: unknown }

// Os avisos sobre os canais de notificação no topo do painel. A consulta de
// canais é independente: a falha vira um aviso próprio, e nunca "nenhum
// canal", porque não saber não é não ter. Carregando, nada.
export function avisosDosCanais(consulta: ConsultaDosCanais): string[] {
  if (consulta.error) return ['Não foi possível conferir os canais de notificação agora.']
  const canais = consulta.data?.items
  if (!canais) return []
  if (canais.length === 0) return ['Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.']
  return [avisoDePausados(canais), avisoSemVerificado(canais), avisoDeFalhando(canais)].filter(
    (aviso): aviso is string => aviso !== null,
  )
}

function avisoDePausados(canais: readonly Canal[]): string | null {
  const pausados = canais.filter((c) => !c.enabled).length
  if (pausados === 0) return null
  const quais = pausados === 1 ? 'desse canal' : 'desses canais'
  return `${plural(pausados, 'canal pausado', 'canais pausados')}. Os avisos ${quais} são descartados.`
}

// Um canal falhando continua verificado e recebendo tentativas (notify).
function avisoSemVerificado(canais: readonly Canal[]): string | null {
  const algumAtivo = canais.some((c) => c.enabled && (c.status === 'verified' || c.status === 'failing'))
  return algumAtivo ? null : 'Nenhum canal verificado está ativo para receber avisos.'
}

function avisoDeFalhando(canais: readonly Canal[]): string | null {
  const falhando = canais.filter((c) => c.enabled && c.status === 'failing').length
  return falhando === 0 ? null : `${plural(falhando, 'canal está', 'canais estão')} falhando na entrega.`
}
