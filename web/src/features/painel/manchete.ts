import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { contarPorEstado, estadoExibido, type EstadoExibido } from '@/lib/estado'
import { dataHora, horaMinuto, plural } from '@/lib/format'
import { ehProblema } from './grupos'

// A manchete do painel (spec do redesign, Telas > 1): uma frase gerada dos
// dados que responde "tem algo quebrado?" de relance.
export type Manchete =
  | { tipo: 'sem-alvos' }
  | { tipo: 'problemas'; quantos: number }
  | { tipo: 'aguardando'; quantos: number }
  | { tipo: 'todos-pausados' }
  | { tipo: 'tudo-no-ar' }

// "Tudo no ar." só quando todo alvo ativo está no ar: um ativo ainda sem
// dados não está "no ar", e os pausados não contam.
export function manchete(itens: readonly ItemDoResumo[]): Manchete {
  if (itens.length === 0) return { tipo: 'sem-alvos' }
  const estados = itens.map((it) => estadoExibido(it.state, it.enabled))
  const problemas = estados.filter(ehProblema).length
  if (problemas > 0) return { tipo: 'problemas', quantos: problemas }
  const semDados = estados.filter((e) => e === 'unknown').length
  if (semDados > 0) return { tipo: 'aguardando', quantos: semDados }
  if (estados.every((e) => e === 'pausado')) return { tipo: 'todos-pausados' }
  return { tipo: 'tudo-no-ar' }
}

// Até dez por extenso, como numa manchete de jornal; depois, o número.
const POR_EXTENSO = ['Zero', 'Um', 'Dois', 'Três', 'Quatro', 'Cinco', 'Seis', 'Sete', 'Oito', 'Nove', 'Dez']

function servicos(n: number): string {
  return `${POR_EXTENSO[n] ?? n} ${n === 1 ? 'serviço' : 'serviços'}`
}

// Sem default: um tipo novo de Manchete sem caso aqui não compila.
export function textoDaManchete(m: Manchete): string {
  switch (m.tipo) {
    case 'sem-alvos':
      return 'Nenhum serviço vigiado ainda.'
    case 'problemas':
      return `${servicos(m.quantos)} ${m.quantos === 1 ? 'pede' : 'pedem'} atenção.`
    case 'aguardando':
      return `${servicos(m.quantos)} ainda sem dados.`
    case 'todos-pausados':
      return 'Todos os alvos estão pausados.'
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

// "Atualizado às 14:32 · 3 de 4 no ar". O total é o dos ativos: um pausado
// não deveria estar no ar.
export function metaDoPainel(itens: readonly ItemDoResumo[], atualizadoEm: Date): string {
  const ativos = itens.filter((it) => it.enabled)
  const noAr = ativos.filter((it) => it.state === 'up').length
  const quantos = ativos.length === 0 ? 'nenhum alvo ativo' : `${noAr} de ${ativos.length} no ar`
  return `Atualizado às ${horaMinuto(atualizadoEm.getTime())} · ${quantos}`
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
