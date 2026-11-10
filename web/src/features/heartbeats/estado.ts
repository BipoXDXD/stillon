import type { Agendamento, EstadoDoHeartbeat, Heartbeat } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'
import { dataHora, duracao } from '@/lib/format'
import { assertUnreachable } from '@/lib/utils'

// O heartbeat na escala de gravidade do painel (forma, cor, ordem e filtro),
// com palavra própria: uma tarefa não fica "fora do ar" (spec da tela, T2 e T3).
const ESTADOS: Record<EstadoDoHeartbeat, { exibido: EstadoExibido; texto: string }> = {
  up: { exibido: 'up', texto: 'Em dia' },
  // Passou do prazo e o alarme ainda não saiu: degradado, que não pede atenção
  // (Ruling do plano da SP1, PR 3).
  grace: { exibido: 'degraded', texto: 'Em tolerância' },
  late: { exibido: 'down', texto: 'Atrasado' },
  failing: { exibido: 'down', texto: 'Falhando' },
  waiting: { exibido: 'unknown', texto: 'Aguardando' },
  paused: { exibido: 'pausado', texto: 'Pausado' },
  // Um estado de versão posterior do servidor: neutro, sem cor de alerta.
  unknown: { exibido: 'unknown', texto: 'Estado desconhecido' },
}

export function estadoDoHeartbeat(e: EstadoDoHeartbeat): EstadoExibido {
  return ESTADOS[e].exibido
}

export function textoDoHeartbeat(e: EstadoDoHeartbeat): string {
  return ESTADOS[e].texto
}

// Atrasado e falhando são derivados na leitura, e quem abre o incidente é o
// avaliador: com ele parado, a tarefa está fora sem incidente nenhum. O
// open_incident_id sozinho deixaria a tela verde a parada toda. Ele entra pela
// tarefa pausada: pausar não encerra o incidente (D13). Vale para a manchete e
// para /incidents.
export function pedeAtencao(hb: Pick<Heartbeat, 'state' | 'open_incident_id'>): boolean {
  return estadoDoHeartbeat(hb.state) === 'down' || hb.open_incident_id !== null
}

// Editar um heartbeat atrasado põe o prazo à frente sem fechar o incidente
// (spec de heartbeats, §5): um prazo futuro não venceu, mesmo no estado late.
export function prazoVencido(hb: Pick<Heartbeat, 'deadline_at'>, agora: Date): boolean {
  return agora.getTime() > Date.parse(hb.deadline_at)
}

// O início da execução em curso. Atrasada, falhando ou pausada, o estado é o que
// importa, e um início aberto ali é de uma execução que não terminou.
export function inicioEmCurso(hb: Pick<Heartbeat, 'state' | 'last_start_at'>): string | null {
  return hb.state === 'up' || hb.state === 'waiting' ? hb.last_start_at : null
}

// Em tolerância o prazo esperado já passou e o deadline_at é a hora do alarme.
// A mesma frase no detalhe e na linha da tabela.
export function textoDaTolerancia(hb: Pick<Heartbeat, 'deadline_at'>): string {
  return `Sem sinal desde o prazo esperado; o alarme sai às ${dataHora(hb.deadline_at)}.`
}

// waiting é nunca ter terminado uma execução: pode já ter havido um início.
export function textoDaEspera(hb: Pick<Heartbeat, 'last_ping_at'>): string {
  return hb.last_ping_at === null ? 'Aguardando o primeiro sinal' : 'Aguardando o primeiro sucesso'
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

export const AGENDAMENTO_NAO_SUPORTADO = 'Agendamento não suportado nesta versão'

export function textoDoAgendamento(a: Agendamento): string {
  switch (a.kind) {
    case 'period':
      return `a cada ${textoDaDuracao(a.period_sec)}`
    case 'cron':
      return `cron ${a.expr}, ${a.timezone}`
    case 'unknown':
      return AGENDAMENTO_NAO_SUPORTADO
    default:
      return assertUnreachable(a)
  }
}
