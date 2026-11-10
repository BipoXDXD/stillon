import type { Heartbeat, Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { casaComBusca, escreverFiltros, heartbeatNosFiltros, lerFiltros, type Filtros } from './filtros'
import { linhaDoAlvo, linhaDoHeartbeat, type LinhaDeMonitor } from './linhas'
import { comOrdem, lerOrdem, ordenarLinhas, type Ordem } from './ordem'

// A lista do painel, na ordem da tela: a mesma regra para a tabela e para o
// Anterior/Próximo do detalhe, que assim nunca discordam. Os itens chegam já
// filtrados por tag e estado (a API filtra); a busca e os filtros dos
// heartbeats valem aqui.
export function linhasDaLista(
  dados: { itens: readonly ItemDoResumo[]; heartbeats: readonly Heartbeat[] },
  filtros: Filtros,
  ordem: Ordem,
  agora: Date,
  incidenteDe: (it: ItemDoResumo) => Incidente | undefined = () => undefined,
): LinhaDeMonitor[] {
  return ordenarLinhas(
    [
      ...dados.itens.filter((it) => casaComBusca(it, filtros.busca)).map((it) => linhaDoAlvo(it, incidenteDe(it), agora)),
      ...dados.heartbeats.filter((hb) => heartbeatNosFiltros(hb, filtros)).map((hb) => linhaDoHeartbeat(hb, agora)),
    ],
    ordem,
  )
}

// Só a parte da URL que descreve a lista (filtros e ordem), normalizada: a
// janela do gráfico e o resto do detalhe ficam de fora.
export function buscaDaLista(p: URLSearchParams): string {
  return comOrdem(escreverFiltros(lerFiltros(p)), lerOrdem(p)).toString()
}

export type Vizinhos = { anterior: LinhaDeMonitor | null; proxima: LinhaDeMonitor | null }

// Quem vem antes e depois do monitor na lista. null: ele não está nela (um
// filtro o esconde, ou ele sumiu), e não há vizinho a oferecer.
export function vizinhosNaLista(linhas: readonly LinhaDeMonitor[], chave: string): Vizinhos | null {
  const i = linhas.findIndex((l) => l.chave === chave)
  if (i === -1) return null
  return { anterior: linhas[i - 1] ?? null, proxima: linhas[i + 1] ?? null }
}
