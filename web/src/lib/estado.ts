import type { EstadoAlvo } from '@/lib/api/schemas'

// O estado que o painel mostra: o do servidor, mais o pausado. Regra de
// domínio, sem visual; o selo, as transições e as telas a consomem.
export type EstadoExibido = EstadoAlvo | 'pausado'

const TEXTOS: Record<EstadoExibido, string> = {
  up: 'No ar',
  degraded: 'Degradado',
  down: 'Fora do ar',
  unknown: 'Sem dados',
  pausado: 'Pausado',
}

export function textoDoEstado(e: EstadoExibido): string {
  return TEXTOS[e]
}

// O servidor diz "unknown" também para alvo desativado; o painel distingue
// pelo enabled (spec do Plano 6 §4.2).
export function estadoExibido(estado: EstadoAlvo, ativo: boolean): EstadoExibido {
  return ativo ? estado : 'pausado'
}

// Quantos alvos há em cada estado exibido; zero onde não há nenhum.
export function contarPorEstado(
  itens: readonly { state: EstadoAlvo; enabled: boolean }[],
): Record<EstadoExibido, number> {
  const conta: Record<EstadoExibido, number> = { up: 0, degraded: 0, down: 0, unknown: 0, pausado: 0 }
  for (const it of itens) conta[estadoExibido(it.state, it.enabled)] += 1
  return conta
}
