import { estadoDoHeartbeat } from '@/features/heartbeats/estado'
import { ESTADOS_DO_FILTRO, type EstadoDoFiltro } from '@/lib/api/cliente'
import type { Heartbeat, ItemDoResumo } from '@/lib/api/schemas'
import { contarPorEstado, estadoExibido, textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { ehTagValida, MAX_TAGS, normalizarTag } from '@/lib/tags'

// Os filtros do painel moram na URL (spec do painel denso): sobrevivem ao
// recarregar e ao botão Voltar. Tag e estado vão à API; a busca é local.
export type Filtros = { tags: string[]; estados: EstadoDoFiltro[]; busca: string }

export const SEM_FILTROS: Filtros = { tags: [], estados: [], busca: '' }

const MAX_BUSCA = 100
const TAGS_NA_LINHA = 3
// O que cabe numa linha da célula de tags: mais que isso, a tag vai para o "+N".
const CARACTERES_NA_LINHA = 18

// A URL é entrada externa: o que a regra não aceita some, e um link velho
// abre a tela com o que ainda vale em vez de um erro.
export function lerFiltros(p: URLSearchParams): Filtros {
  const tags = [...new Set(p.getAll('tag').map(normalizarTag))].filter(ehTagValida).slice(0, MAX_TAGS)
  const estados = ESTADOS_DO_FILTRO.filter((e) => p.getAll('estado').includes(e))
  return { tags, estados, busca: (p.get('busca') ?? '').slice(0, MAX_BUSCA) }
}

export function escreverFiltros(f: Filtros): URLSearchParams {
  const p = new URLSearchParams()
  for (const tag of f.tags) p.append('tag', tag)
  for (const estado of f.estados) p.append('estado', estado)
  if (f.busca) p.set('busca', f.busca)
  return p
}

export function temFiltroDeApi(f: Filtros): boolean {
  return f.tags.length > 0 || f.estados.length > 0
}

export function temFiltro(f: Filtros): boolean {
  return temFiltroDeApi(f) || f.busca !== ''
}

export function alternar<T>(lista: readonly T[], valor: T): T[] {
  return lista.includes(valor) ? lista.filter((v) => v !== valor) : [...lista, valor]
}

export function estadoDoFiltro(e: EstadoExibido): EstadoDoFiltro {
  return e === 'pausado' ? 'paused' : e
}

export function estadoExibidoDoFiltro(e: EstadoDoFiltro): EstadoExibido {
  return e === 'paused' ? 'pausado' : e
}

export function estadoDoItem(it: ItemDoResumo): EstadoDoFiltro {
  return estadoDoFiltro(estadoExibido(it.state, it.enabled))
}

// Sem acento nem caixa: quem digita "producao" acha "produção".
const dobrar = (s: string) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase().trim()

export function casaComBusca(it: { name: string; tags: readonly string[] }, busca: string): boolean {
  const termo = dobrar(busca)
  return [it.name, ...it.tags].some((s) => dobrar(s).includes(termo))
}

// O heartbeat não passa pela API de filtro: a regra é a mesma, aqui. Todas as
// tags pedidas têm de estar nele, como no alvo.
export function heartbeatNosFiltros(hb: Heartbeat, f: Filtros): boolean {
  if (!f.tags.every((tag) => hb.tags.includes(tag))) return false
  if (f.estados.length > 0 && !f.estados.includes(estadoDoFiltro(estadoDoHeartbeat(hb.state)))) return false
  return casaComBusca(hb, f.busca)
}

// Quantos há em cada estado no conjunto inteiro, para os botões da barra.
export function contarDoConjunto(itens: readonly ItemDoResumo[], heartbeats: readonly Heartbeat[]): Record<EstadoExibido, number> {
  const conta = contarPorEstado(itens)
  for (const hb of heartbeats) conta[estadoDoHeartbeat(hb.state)] += 1
  return conta
}

export type ContagemDeTag = { tag: string; quantos: number }

export function contarTags(itens: readonly { tags: readonly string[] }[]): ContagemDeTag[] {
  const conta = new Map<string, number>()
  for (const it of itens) for (const tag of it.tags) conta.set(tag, (conta.get(tag) ?? 0) + 1)
  return [...conta]
    .map(([tag, quantos]) => ({ tag, quantos }))
    .sort((a, b) => b.quantos - a.quantos || a.tag.localeCompare(b.tag, 'pt-BR'))
}

export function tagsVisiveis(tags: readonly string[]): { visiveis: string[]; resto: string[] } {
  const visiveis: string[] = []
  let caracteres = 0
  for (const tag of tags) {
    caracteres += [...tag].length
    if (visiveis.length === TAGS_NA_LINHA || (visiveis.length > 0 && caracteres > CARACTERES_NA_LINHA)) break
    visiveis.push(tag)
  }
  return { visiveis, resto: tags.slice(visiveis.length) }
}

// "Tags prod e api · Fora do ar ou Pausado · Busca “site”", no estado sem resultado.
export function descreverFiltros(f: Filtros): string {
  const partes: string[] = []
  if (f.tags.length) partes.push(`${f.tags.length === 1 ? 'Tag' : 'Tags'} ${f.tags.join(' e ')}`)
  if (f.estados.length) partes.push(f.estados.map((e) => textoDoEstado(estadoExibidoDoFiltro(e))).join(' ou '))
  if (f.busca) partes.push(`Busca “${f.busca}”`)
  return partes.join(' · ')
}
