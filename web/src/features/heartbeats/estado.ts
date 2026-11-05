import type { Agendamento, EstadoDoHeartbeat } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'

// O heartbeat na escala de gravidade do painel (forma, cor, ordem e filtro),
// com palavra própria: uma tarefa não fica "fora do ar" (spec da tela, T2 e T3).
const ESTADOS: Record<EstadoDoHeartbeat, { exibido: EstadoExibido; texto: string }> = {
  up: { exibido: 'up', texto: 'Em dia' },
  late: { exibido: 'down', texto: 'Atrasado' },
  failing: { exibido: 'down', texto: 'Falhando' },
  waiting: { exibido: 'unknown', texto: 'Aguardando' },
  paused: { exibido: 'pausado', texto: 'Pausado' },
}

export function estadoDoHeartbeat(e: EstadoDoHeartbeat): EstadoExibido {
  return ESTADOS[e].exibido
}

export function textoDoHeartbeat(e: EstadoDoHeartbeat): string {
  return ESTADOS[e].texto
}

export type Unidade = 'dias' | 'horas' | 'minutos' | 'segundos'

export const SEGUNDOS_DA_UNIDADE: Record<Unidade, number> = { dias: 86400, horas: 3600, minutos: 60, segundos: 1 }

const DA_MAIOR_PARA_A_MENOR: readonly Unidade[] = ['dias', 'horas', 'minutos']

// A maior unidade que divide o valor exato: 5400 s é 90 min, nunca "1,5 h".
export function partirDuracao(segundos: number): { quantidade: number; unidade: Unidade } {
  const unidade = DA_MAIOR_PARA_A_MENOR.find((u) => segundos % SEGUNDOS_DA_UNIDADE[u] === 0) ?? 'segundos'
  return { quantidade: segundos / SEGUNDOS_DA_UNIDADE[unidade], unidade }
}

const ABREVIADA: Record<Unidade, string> = { dias: 'dias', horas: 'h', minutos: 'min', segundos: 's' }

export function textoDaDuracao(segundos: number): string {
  const { quantidade, unidade } = partirDuracao(segundos)
  return `${quantidade} ${quantidade === 1 && unidade === 'dias' ? 'dia' : ABREVIADA[unidade]}`
}

export function textoDoAgendamento(a: Agendamento): string {
  return a.kind === 'period' ? `a cada ${textoDaDuracao(a.period_sec)}` : `cron ${a.expr}, ${a.timezone}`
}
