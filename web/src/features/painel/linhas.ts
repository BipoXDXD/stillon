import { estadoDoHeartbeat, inicioEmCurso, prazoVencido, tempoDeExecucao, textoDaEspera, textoDaTolerancia, textoDoHeartbeat } from '@/features/heartbeats/estado'
import { textoDaCausa } from '@/lib/api/mensagens'
import type { Heartbeat, HoraDoSpark, Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { duracao, haQuantoAteAgora, milissegundos } from '@/lib/format'
import { assertUnreachable } from '@/lib/utils'
import { detalheDoProblema, ehProblema, linhaSemDados } from './grupos'

// O que a tabela de monitores desenha. Ela não conhece o item da API: um
// segundo tipo de monitor entra como outro membro desta união, com o mapeador
// dele, sem tocar nas colunas (spec do painel denso). Todo switch sobre `tipo`
// termina em tipoSemCaso: um membro novo sem caso não compila.
type LinhaComum = {
  chave: string
  nome: string
  href: string
  estado: EstadoExibido
  textoDoEstado: string
  situacao: string
  tags: string[]
  uptime24h: number | null
  historico: HoraDoSpark[]
  incidenteHref: string | null
}

type LinhaHttp = LinhaComum & { tipo: 'http'; latenciaMs: number | null }

type LinhaHeartbeat = LinhaComum & { tipo: 'heartbeat' }

export type LinhaDeMonitor = LinhaHttp | LinhaHeartbeat

export function linhaDoAlvo(it: ItemDoResumo, incidente: Incidente | undefined, agora: Date): LinhaDeMonitor {
  const estado = estadoExibido(it.state, it.enabled)
  return {
    tipo: 'http',
    chave: `http:${it.id}`,
    nome: it.name,
    href: `/targets/${it.id}`,
    estado,
    textoDoEstado: textoDoEstado(estado),
    situacao: situacaoDoAlvo(it, estado, incidente, agora),
    tags: it.tags,
    latenciaMs: it.last_latency_ms,
    uptime24h: it.uptime_24h,
    historico: it.spark,
    incidenteHref: it.open_incident_id ? '/incidents?status=open' : null,
  }
}

function situacaoDoAlvo(it: ItemDoResumo, estado: EstadoExibido, incidente: Incidente | undefined, agora: Date): string {
  switch (estado) {
    case 'down':
    case 'degraded':
      return detalheDoProblema(it, incidente, agora)
    case 'up':
      return it.last_checked_at === null ? 'Nenhum check ainda' : `Check ${haQuantoAteAgora(it.last_checked_at, agora)}`
    case 'unknown':
    case 'pausado':
      return linhaSemDados(it, agora)
  }
}

// Sem uptime e sem as 24 horas: o heartbeat não tem nada disso, e medida
// inventada mentiria (spec da tela, T5).
export function linhaDoHeartbeat(hb: Heartbeat, agora: Date): LinhaDeMonitor {
  return {
    tipo: 'heartbeat',
    chave: `heartbeat:${hb.id}`,
    nome: hb.name,
    href: `/heartbeats/${hb.id}`,
    estado: estadoDoHeartbeat(hb.state),
    textoDoEstado: textoDoHeartbeat(hb.state),
    situacao: situacaoDoHeartbeat(hb, agora),
    tags: hb.tags,
    uptime24h: null,
    historico: [],
    incidenteHref: hb.open_incident_id ? `/heartbeats/${hb.id}` : null,
  }
}

function situacaoDoHeartbeat(hb: Heartbeat, agora: Date): string {
  switch (hb.state) {
    case 'late':
      return textoDoAtraso(hb, agora)
    case 'failing':
      return textoDaCausa('heartbeat_failed')
    case 'up':
      return textoEmDia(hb, agora)
    case 'grace':
      return textoDaTolerancia(hb)
    case 'waiting':
      return textoDeExecucao(hb, agora) ?? textoDaEspera(hb)
    case 'paused':
      return 'Pausado'
    case 'unknown':
      return textoDoHeartbeat(hb.state)
    default:
      return assertUnreachable(hb.state)
  }
}

function textoDeExecucao(hb: Heartbeat, agora: Date): string | null {
  const inicio = inicioEmCurso(hb)
  return inicio === null ? null : `Em execução, começou ${haQuantoAteAgora(inicio, agora)}`
}

function textoEmDia(hb: Heartbeat, agora: Date): string {
  const emExecucao = textoDeExecucao(hb, agora)
  if (emExecucao !== null) return emExecucao
  if (hb.last_ping_at === null) return 'Nenhum sinal ainda'
  const sinal = `Sinal ${haQuantoAteAgora(hb.last_ping_at, agora)}`
  return hb.last_runtime_ms === null ? sinal : `${sinal} · levou ${tempoDeExecucao(hb.last_runtime_ms)}`
}

// Sem prazo vencido, não há duração a dizer.
function textoDoAtraso(hb: Heartbeat, agora: Date): string {
  const causa = textoDaCausa('heartbeat_late')
  return prazoVencido(hb, agora) ? `${causa} · há ${duracao(agora.getTime() - Date.parse(hb.deadline_at))}` : causa
}

// O número da coluna de medida: a latência do último check, no monitor HTTP.
// Um tipo novo sem caso chega ao default sem virar never, e não compila.
export function medidaDaLinha(linha: LinhaDeMonitor): string {
  const { tipo } = linha
  switch (tipo) {
    case 'http':
      return milissegundos(linha.latenciaMs)
    case 'heartbeat':
      return '—'
    default:
      return tipoSemCaso(tipo)
  }
}

// A latência como número, para ordenar: null é quem não tem a medida.
export function latenciaDaLinha(linha: LinhaDeMonitor): number | null {
  const { tipo } = linha
  switch (tipo) {
    case 'http':
      return linha.latenciaMs
    case 'heartbeat':
      return null
    default:
      return tipoSemCaso(tipo)
  }
}

export function tipoSemCaso(tipo: never): never {
  throw new Error(`Tipo de monitor sem caso: ${String(tipo)}`)
}

// No celular a coluna Situação some: quem está bem fica em uma altura, e o
// resto ganha a frase abaixo do nome (spec, Mobile).
export function situacaoNaLinhaDoNome(linha: LinhaDeMonitor): string | null {
  return ehProblema(linha.estado) || linha.estado === 'unknown' || linha.estado === 'pausado' ? linha.situacao : null
}
