import { textoDaCausa } from '@/lib/api/mensagens'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, type EstadoExibido } from '@/lib/estado'
import { duracao, haQuanto, milissegundos } from '@/lib/format'

// A ordem do painel (spec do redesign, Telas > 1): os problemas primeiro, em
// cartões; o que funciona, em linhas; sem dados e pausados, em texto.
// Funções puras: a tela só as chama.
export type GruposDoPainel = {
  problemas: ItemDoResumo[]
  funcionando: ItemDoResumo[]
  semDadosOuPausados: ItemDoResumo[]
}

const GRAVIDADE: Record<EstadoExibido, number> = { down: 0, degraded: 1, up: 2, unknown: 3, pausado: 4 }

// numeric: "Serviço 2" antes de "Serviço 10"; base: maiúscula e acento não
// separam. A ordem do servidor (id decrescente) não ajuda a achar um alvo
// entre 30.
const PELO_NOME = new Intl.Collator('pt-BR', { sensitivity: 'base', numeric: true })

const exibido = (it: ItemDoResumo) => estadoExibido(it.state, it.enabled)

export function ehProblema(e: EstadoExibido): boolean {
  return e === 'down' || e === 'degraded'
}

export function agruparPorGravidade(itens: readonly ItemDoResumo[]): GruposDoPainel {
  const ordenados = [...itens].sort(
    (a, b) => GRAVIDADE[exibido(a)] - GRAVIDADE[exibido(b)] || PELO_NOME.compare(a.name, b.name),
  )
  return {
    problemas: ordenados.filter((it) => ehProblema(exibido(it))),
    funcionando: ordenados.filter((it) => exibido(it) === 'up'),
    semDadosOuPausados: ordenados.filter((it) => exibido(it) === 'unknown' || exibido(it) === 'pausado'),
  }
}

// A linha de baixo do cartão de problema: com incidente aberto, a causa e
// há quanto tempo; sem ele, a latência (degradado) ou o último check (fora
// do ar antes de o incidente abrir).
export function detalheDoProblema(it: ItemDoResumo, incidente: Incidente | undefined, agora: Date): string {
  if (incidente) {
    return `${textoDaCausa(incidente.cause)} · há ${duracao(agora.getTime() - Date.parse(incidente.started_at))}`
  }
  if (it.state === 'degraded') {
    return it.last_latency_ms === null ? 'Lento no último check' : `Latência de ${milissegundos(it.last_latency_ms)} no último check`
  }
  return it.last_checked_at === null ? 'Nenhum check ainda' : `Último check ${haQuanto(it.last_checked_at, agora)}`
}

// Uma linha de texto por alvo, sem gráfico (auditoria P7).
export function linhaSemDados(it: ItemDoResumo, agora: Date): string {
  if (!it.enabled) return 'Pausado'
  return it.last_checked_at === null
    ? 'Sem dados · nunca verificado'
    : `Sem dados · último check ${haQuanto(it.last_checked_at, agora)}`
}
