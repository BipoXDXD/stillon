import type { HoraDoSpark, Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, type EstadoExibido } from '@/lib/estado'
import { haQuantoAteAgora, milissegundos } from '@/lib/format'
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
  situacao: string
  tags: string[]
  uptime24h: number | null
  historico: HoraDoSpark[]
  incidenteHref: string | null
}

type LinhaHttp = LinhaComum & { tipo: 'http'; latenciaMs: number | null }

export type LinhaDeMonitor = LinhaHttp

export function linhaDoAlvo(it: ItemDoResumo, incidente: Incidente | undefined, agora: Date): LinhaDeMonitor {
  const estado = estadoExibido(it.state, it.enabled)
  return {
    tipo: 'http',
    chave: `http:${it.id}`,
    nome: it.name,
    href: `/targets/${it.id}`,
    estado,
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

// O número da coluna de medida: a latência do último check, no monitor HTTP.
// Um tipo novo sem caso chega ao default sem virar never, e não compila.
export function medidaDaLinha(linha: LinhaDeMonitor): string {
  switch (linha.tipo) {
    case 'http':
      return milissegundos(linha.latenciaMs)
    default:
      return tipoSemCaso(linha.tipo)
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
