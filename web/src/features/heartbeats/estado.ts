import type { Agendamento, EstadoDoHeartbeat, Heartbeat } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'
import { duracao } from '@/lib/format'

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

// Atrasado e falhando são derivados na leitura, e quem abre o incidente é o
// avaliador: com ele parado, a tarefa está fora sem incidente nenhum. O
// open_incident_id sozinho deixaria a tela verde a parada toda.
export function pedeAtencao(hb: Pick<Heartbeat, 'state' | 'open_incident_id'>): boolean {
  return estadoDoHeartbeat(hb.state) === 'down' || hb.open_incident_id !== null
}

// Editar um heartbeat atrasado põe o prazo à frente sem fechar o incidente
// (spec de heartbeats, §5): um prazo futuro não venceu, mesmo no estado late.
export function prazoVencido(hb: Pick<Heartbeat, 'deadline_at'>, agora: Date): boolean {
  return agora.getTime() > Date.parse(hb.deadline_at)
}

const MS_POR_SEGUNDO = 1000
const MS_POR_MINUTO = 60_000

export function tempoDeExecucao(ms: number): string {
  if (ms < MS_POR_SEGUNDO) return `${ms} ms`
  return ms < MS_POR_MINUTO ? `${Math.round(ms / MS_POR_SEGUNDO)} s` : duracao(ms)
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
