import { keepPreviousData, onlineManager, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useSyncExternalStore } from 'react'
import { useApi } from '@/app/contexto'
import type { FiltroDoResumo } from './cliente'
import type { Incidente, ItemDoResumo, ListaDeIncidentes } from './schemas'

const MS_DA_PREVIA = 60_000

// As chaves e os intervalos de atualização (spec §11.4).
export const chaves = {
  me: ['me'] as const,
  previaDoCron: (expr: string, timezone: string) => ['previa-do-cron', expr, timezone] as const,
  // Prefixos: invalidar um deles alcança tudo que está abaixo.
  todosOsAlvos: ['targets'] as const,
  todosOsIncidentes: ['incidents'] as const,
  resumo: ['targets', 'summary'] as const,
  // Abaixo da chave do resumo: quem invalida o resumo invalida as duas.
  resumoFiltrado: (f: FiltroDoResumo) => ['targets', 'summary', f.tags, f.estados] as const,
  alvos: ['targets', 'list'] as const,
  alvo: (id: string) => ['targets', id] as const,
  serie: (id: string, janela: string) => ['targets', id, 'checks', janela] as const,
  // O limite entra na chave: páginas de tamanhos diferentes não dividem o cache.
  incidentes: (filtro: string, alvo?: string, limite?: number) => ['incidents', filtro, alvo ?? '', limite ?? 0] as const,
  heartbeats: ['heartbeats'] as const,
  heartbeat: (id: string) => ['heartbeats', id] as const,
  eventosDoHeartbeat: (id: string) => ['heartbeats', id, 'events'] as const,
  incidentesDoHeartbeat: (id: string) => ['heartbeats', id, 'incidents'] as const,
  canais: ['channels'] as const,
  paginas: ['status-pages'] as const,
  sessoes: ['sessions'] as const,
}

// Os intervalos do poll. O resumo é o mais curto: é
// ele que responde "tem algo quebrado?" no painel.
const INTERVALO_DO_RESUMO_MS = 15_000
const INTERVALO_DOS_INCIDENTES_MS = 30_000
const INTERVALO_DO_ALVO_MS = 60_000

const INTERVALO_DA_SERIE_MS = 60_000
const INTERVALO_DOS_CANAIS_MS = 60_000
const INTERVALO_DOS_RESOLVIDOS_MS = 60_000

// A primeira página de uma lista paginada não leva cursor. A anotação dá ao
// useInfiniteQuery o tipo do cursor (string | undefined) sem uma assertion.
const PRIMEIRA_PAGINA: string | undefined = undefined

const estaSemRede = () => !onlineManager.isOnline()

// Sem rede o React Query pausa as consultas em vez de falhar: nenhum erro
// nasce, e quem só olha a consulta acha que o que está na tela é atual.
export function useSemRede(): boolean {
  return useSyncExternalStore(onlineManager.subscribe, estaSemRede)
}

export function useMe() {
  const api = useApi()
  return useQuery({ queryKey: chaves.me, queryFn: api.me, staleTime: Infinity })
}

export function useResumo() {
  const api = useApi()
  return useQuery({ queryKey: chaves.resumo, queryFn: () => api.resumo(), refetchInterval: INTERVALO_DO_RESUMO_MS })
}

// A tabela com filtro de tag ou estado. A consulta completa continua em poll:
// a manchete, as contagens e a lista de tags falam do conjunto inteiro. Até a
// resposta chegar, fica o que estava na tela (spec do painel denso, Estados).
export function useResumoFiltrado(filtro: FiltroDoResumo, ativo: boolean, naTela: { items: ItemDoResumo[] }) {
  const api = useApi()
  return useQuery({
    queryKey: chaves.resumoFiltrado(filtro),
    queryFn: () => api.resumo(filtro),
    refetchInterval: INTERVALO_DO_RESUMO_MS,
    enabled: ativo,
    placeholderData: (anterior) => anterior ?? naTela,
  })
}

export function useAlvos() {
  const api = useApi()
  return useQuery({ queryKey: chaves.alvos, queryFn: api.alvos })
}

export function useAlvo(id: string) {
  const api = useApi()
  return useQuery({ queryKey: chaves.alvo(id), queryFn: () => api.alvo(id), refetchInterval: INTERVALO_DO_ALVO_MS })
}

export function useSerie(id: string, janela: string) {
  const api = useApi()
  return useQuery({
    queryKey: chaves.serie(id, janela),
    queryFn: () => api.serie(id, janela),
    refetchInterval: INTERVALO_DA_SERIE_MS,
    placeholderData: keepPreviousData,
  })
}

export function useIncidentes(filtro: 'open' | 'resolved' | 'all', alvo?: string, limite?: number) {
  const api = useApi()
  return useInfiniteQuery({
    queryKey: chaves.incidentes(filtro, alvo, limite),
    queryFn: ({ pageParam }) =>
      api.incidentes({
        status: filtro === 'all' ? undefined : filtro,
        cursor: pageParam,
        targetId: alvo,
        limit: limite,
      }),
    initialPageParam: PRIMEIRA_PAGINA,
    getNextPageParam: (ultima) => ultima.next_cursor,
    refetchInterval: pollSoComUmaPagina,
  })
}

// O poll de uma infinite query refaz todas as páginas carregadas, em série,
// e gasta o rate limit por usuário. O maxPages não serve: exige lista
// bidirecional, e o cursor só anda para frente. Então o poll só roda com
// uma página; com mais, a lista se atualiza no foco e no reconhecer.
function pollSoComUmaPagina(q: { state: { data?: { pages: unknown[] } } }): number | false {
  return (q.state.data?.pages.length ?? 0) > 1 ? false : INTERVALO_DOS_INCIDENTES_MS
}

// Os incidentes abertos: uma página de até 100, o teto da rota. Uma consulta
// só, dividida pelo contador da navegação, pelos
// cartões do painel e pelo resumo da tela de incidentes: cada um lê o que
// precisa pelo select. A chave fica sob ['incidents'], e o reconhecer, que
// invalida ['incidents'], atualiza todos.
const LIMITE_DOS_ABERTOS = 100

export type Abertos = { itens: Incidente[]; completa: boolean }

function useConsultaDosAbertos<T>(select: (d: ListaDeIncidentes) => T) {
  const api = useApi()
  return useQuery({
    queryKey: [...chaves.incidentes('open', undefined, LIMITE_DOS_ABERTOS), 'abertos'],
    queryFn: () => api.incidentes({ status: 'open', limit: LIMITE_DOS_ABERTOS }),
    refetchInterval: INTERVALO_DOS_INCIDENTES_MS,
    select,
  })
}

const CONTADOR_CHEIO = 99

// Mais que uma página vira "99+". As tarefas que pedem atenção somam: /incidents
// não as traz, e o contador não fica mudo com tarefa fora.
function textoDoContador(d: ListaDeIncidentes, tarefas: number): string {
  const total = d.items.length + tarefas
  return d.next_cursor || total > CONTADOR_CHEIO ? `${CONTADOR_CHEIO}+` : String(total)
}

function listarAbertos(d: ListaDeIncidentes): Abertos {
  return { itens: d.items, completa: d.next_cursor === undefined }
}

export function useAbertos(tarefas: number): string | undefined {
  const alvos = useConsultaDosAbertos((d) => d)
  return alvos.data && textoDoContador(alvos.data, tarefas)
}

export function useIncidentesAbertos() {
  return useConsultaDosAbertos(listarAbertos)
}

// Os resolvidos mais recentes, para o "Tudo no ar." do painel dizer quando
// terminou o último incidente. A rota ordena pela abertura (o id é UUIDv7),
// não pelo fim: entre os 20 abertos mais recentemente, o de resolved_at
// maior é o último a terminar, salvo um incidente aberto antes deles e
// encerrado depois de todos (spec do redesign, sem campo novo na API). Só
// roda com o painel todo no ar.
const LIMITE_DOS_RESOLVIDOS = 20

export function useResolvidosRecentes(ativa: boolean) {
  const api = useApi()
  return useQuery({
    queryKey: [...chaves.incidentes('resolved', undefined, LIMITE_DOS_RESOLVIDOS), 'ultimo'],
    queryFn: () => api.incidentes({ status: 'resolved', limit: LIMITE_DOS_RESOLVIDOS }),
    enabled: ativa,
    refetchInterval: INTERVALO_DOS_RESOLVIDOS_MS,
  })
}

// No ritmo do resumo: a manchete fala dos dois (spec da tela, A manchete).
export function useHeartbeats() {
  const api = useApi()
  return useQuery({ queryKey: chaves.heartbeats, queryFn: api.heartbeats, refetchInterval: INTERVALO_DO_RESUMO_MS })
}

export function useHeartbeat(id: string) {
  const api = useApi()
  return useQuery({ queryKey: chaves.heartbeat(id), queryFn: () => api.heartbeat(id), refetchInterval: INTERVALO_DO_RESUMO_MS })
}

// Sem retry: 400 é a resposta para quem ainda está digitando. A expressão não
// muda de resposta na escala de um minuto, e o cache poupa o vaivém do campo.
export function usePreviaDoCron(expr: string, timezone: string) {
  const api = useApi()
  return useQuery({
    queryKey: chaves.previaDoCron(expr, timezone),
    queryFn: () => api.previaDoCron(expr, timezone),
    enabled: expr !== '' && timezone !== '',
    retry: false,
    staleTime: MS_DA_PREVIA,
  })
}

export function useEventosDoHeartbeat(id: string) {
  const api = useApi()
  return useInfiniteQuery({
    queryKey: chaves.eventosDoHeartbeat(id),
    queryFn: ({ pageParam }) => api.eventosDoHeartbeat(id, pageParam),
    initialPageParam: PRIMEIRA_PAGINA,
    getNextPageParam: (ultima) => ultima.next_cursor,
    refetchInterval: pollSoComUmaPagina,
  })
}

export function useIncidentesDoHeartbeat(id: string) {
  const api = useApi()
  return useInfiniteQuery({
    queryKey: chaves.incidentesDoHeartbeat(id),
    queryFn: ({ pageParam }) => api.incidentesDoHeartbeat(id, pageParam),
    initialPageParam: PRIMEIRA_PAGINA,
    getNextPageParam: (ultima) => ultima.next_cursor,
    refetchInterval: pollSoComUmaPagina,
  })
}

export function useCanais() {
  const api = useApi()
  return useQuery({ queryKey: chaves.canais, queryFn: api.canais, refetchInterval: INTERVALO_DOS_CANAIS_MS })
}

export function usePaginas() {
  const api = useApi()
  return useQuery({ queryKey: chaves.paginas, queryFn: api.paginas })
}

export function useSessoes() {
  const api = useApi()
  return useQuery({ queryKey: chaves.sessoes, queryFn: api.sessoes })
}
