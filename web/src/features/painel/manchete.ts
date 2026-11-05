import { estadoDoHeartbeat } from '@/features/heartbeats/estado'
import type { EstadoDoHeartbeat, Heartbeat, Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { contarPorEstado, estadoExibido, type EstadoExibido } from '@/lib/estado'
import { dataHora, horaMinuto, plural } from '@/lib/format'
import { ehProblema } from './grupos'

// A manchete do painel (spec do redesign, Telas > 1): uma frase gerada dos
// dados que responde "tem algo quebrado?" de relance.
export type Manchete =
  | { tipo: 'sem-alvos' }
  | { tipo: 'problemas'; quantos: number; tarefas: number }
  | { tipo: 'aguardando'; quantos: number; tarefas: number }
  | { tipo: 'tarefas-sem-confirmacao' }
  | { tipo: 'todos-pausados' }
  | { tipo: 'tudo-no-ar' }

// As tarefas que o painel conhece. confirmadas é false quando a consulta de
// heartbeats falhou: não saber não é "tudo no ar". Sempre obrigatório na
// manchete: uma chamada que as esquecesse ficaria verde com tarefa atrasada.
export type Tarefas = { itens: readonly Heartbeat[]; confirmadas: boolean }

// A manchete fala do conjunto inteiro, alvos e tarefas (spec da tela, A
// manchete). "Tudo no ar." só quando todo ativo está bem e a resposta das
// tarefas chegou: um ativo ainda sem dados não está "no ar", e os pausados
// não contam.
export function manchete(itens: readonly ItemDoResumo[], tarefas: Tarefas): Manchete {
  const alvos = itens.map((it) => estadoExibido(it.state, it.enabled))
  const dasTarefas = tarefas.itens.map((hb) => estadoDoHeartbeat(hb.state))
  const quantos = alvos.filter(ehProblema).length
  const tarefasComProblema = dasTarefas.filter(ehProblema).length
  if (quantos + tarefasComProblema > 0) return { tipo: 'problemas', quantos, tarefas: tarefasComProblema }
  if (!tarefas.confirmadas) return { tipo: 'tarefas-sem-confirmacao' }
  const todos = [...alvos, ...dasTarefas]
  if (todos.length === 0) return { tipo: 'sem-alvos' }
  const semDados = alvos.filter((e) => e === 'unknown').length
  const semSinal = dasTarefas.filter((e) => e === 'unknown').length
  if (semDados + semSinal > 0) return { tipo: 'aguardando', quantos: semDados, tarefas: semSinal }
  if (todos.every((e) => e === 'pausado')) return { tipo: 'todos-pausados' }
  return { tipo: 'tudo-no-ar' }
}

// Até dez por extenso, como numa manchete de jornal; depois, o número.
const POR_EXTENSO = ['Zero', 'Um', 'Dois', 'Três', 'Quatro', 'Cinco', 'Seis', 'Sete', 'Oito', 'Nove', 'Dez']
const FEMININOS: Record<number, string> = { 1: 'Uma', 2: 'Duas' }

function servicos(n: number): string {
  return `${POR_EXTENSO[n] ?? n} ${n === 1 ? 'serviço' : 'serviços'}`
}

function nomeDasTarefas(n: number): string {
  return `${FEMININOS[n] ?? POR_EXTENSO[n] ?? n} ${n === 1 ? 'tarefa' : 'tarefas'}`
}

function primeiraMinuscula(s: string): string {
  return s.charAt(0).toLowerCase() + s.slice(1)
}

// "Um serviço e uma tarefa", ou o que houver dos dois.
function sujeito(quantos: number, tarefas: number): string {
  if (tarefas === 0) return servicos(quantos)
  if (quantos === 0) return nomeDasTarefas(tarefas)
  return `${servicos(quantos)} e ${primeiraMinuscula(nomeDasTarefas(tarefas))}`
}

function fraseDeEspera(quantos: number, tarefas: number): string {
  if (tarefas === 0) return `${servicos(quantos)} ainda sem dados.`
  if (quantos === 0) return `${nomeDasTarefas(tarefas)} ainda sem sinal.`
  return `${servicos(quantos)} ainda sem dados e ${primeiraMinuscula(nomeDasTarefas(tarefas))} sem sinal.`
}

// Sem default: um tipo novo de Manchete sem caso aqui não compila.
export function textoDaManchete(m: Manchete): string {
  switch (m.tipo) {
    case 'sem-alvos':
      return 'Nenhum serviço vigiado ainda.'
    case 'problemas':
      return `${sujeito(m.quantos, m.tarefas)} ${m.quantos + m.tarefas === 1 ? 'pede' : 'pedem'} atenção.`
    case 'aguardando':
      return fraseDeEspera(m.quantos, m.tarefas)
    case 'tarefas-sem-confirmacao':
      return 'Não foi possível conferir as tarefas.'
    case 'todos-pausados':
      return 'Todos os monitores estão pausados.'
    case 'tudo-no-ar':
      return 'Tudo no ar.'
  }
}

const ORDEM: readonly [EstadoExibido, string, string][] = [
  ['down', 'fora do ar', 'fora do ar'],
  ['degraded', 'degradado', 'degradados'],
  ['up', 'no ar', 'no ar'],
  ['unknown', 'sem dados', 'sem dados'],
  ['pausado', 'pausado', 'pausados'],
]

// A frase de detalhe da manchete, por gravidade: "1 fora do ar, 2 degradados, 5 no ar".
export function resumoEmTexto(itens: readonly ItemDoResumo[]): string {
  const conta = contarPorEstado(itens)
  return ORDEM.flatMap(([e, um, varios]) => (conta[e] ? [plural(conta[e], um, varios)] : [])).join(', ')
}

const ORDEM_DAS_TAREFAS: readonly [EstadoDoHeartbeat, string, string][] = [
  ['failing', 'com falha', 'com falha'],
  ['late', 'atrasada', 'atrasadas'],
  ['up', 'em dia', 'em dia'],
  ['waiting', 'aguardando', 'aguardando'],
  ['paused', 'pausada', 'pausadas'],
]

function resumoDasTarefas(tarefas: readonly Heartbeat[]): string {
  const frases = ORDEM_DAS_TAREFAS.flatMap(([e, um, varios]) => {
    const n = tarefas.filter((hb) => hb.state === e).length
    return n ? [plural(n, um, varios)] : []
  })
  return frases.length ? `Tarefas: ${frases.join(', ')}` : ''
}

// A linha abaixo da manchete: "1 fora do ar, 5 no ar. Tarefas: 1 atrasada."
export function detalheDaManchete(itens: readonly ItemDoResumo[], tarefas: readonly Heartbeat[]): string {
  return `${[resumoEmTexto(itens), resumoDasTarefas(tarefas)].filter(Boolean).join('. ')}.`
}

// "Atualizado às 14:32 · 3 de 4 no ar · 2 de 3 tarefas em dia". O total é o
// dos ativos: um pausado não deveria estar no ar. Sem nada do tipo, ele some.
export function metaDoPainel(itens: readonly ItemDoResumo[], tarefas: readonly Heartbeat[], atualizadoEm: Date): string {
  const ativos = itens.filter((it) => it.enabled)
  const noAr = ativos.filter((it) => it.state === 'up').length
  const tarefasAtivas = tarefas.filter((hb) => hb.enabled)
  const emDia = tarefasAtivas.filter((hb) => hb.state === 'up').length
  const partes = [`Atualizado às ${horaMinuto(atualizadoEm.getTime())}`]
  if (ativos.length > 0) partes.push(`${noAr} de ${ativos.length} no ar`)
  else if (tarefas.length === 0) partes.push('nenhum alvo ativo')
  if (tarefasAtivas.length > 0) partes.push(`${emDia} de ${tarefasAtivas.length} ${tarefasAtivas.length === 1 ? 'tarefa' : 'tarefas'} em dia`)
  return partes.join(' · ')
}

type Resolvido = Incidente & { resolved_at: string }

// O último incidente a terminar. A rota ordena pela abertura, então o
// primeiro da lista não serve.
export function ultimoResolvido(itens: readonly Incidente[]): Resolvido | null {
  const resolvidos = itens.filter((i): i is Resolvido => i.resolved_at !== null)
  return resolvidos.reduce<Resolvido | null>(
    (ultimo, i) => (ultimo === null || Date.parse(i.resolved_at) > Date.parse(ultimo.resolved_at) ? i : ultimo),
    null,
  )
}

// A frase do "Tudo no ar.". Sem resposta (carregando ou falha), nada: não
// saber não é "nenhum incidente".
export function fraseDoUltimoResolvido(itens: readonly Incidente[] | undefined): string | null {
  if (itens === undefined) return null
  const ultimo = ultimoResolvido(itens)
  return ultimo ? `Último incidente resolvido em ${dataHora(ultimo.resolved_at)}.` : 'Nenhum incidente registrado.'
}
