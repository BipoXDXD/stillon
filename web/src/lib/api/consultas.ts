import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useApi } from '@/app/contexto'
import type { Incidente, ListaDeIncidentes } from './schemas'

// As chaves e os intervalos de atualização (spec §11.4; spec do Plano 6 §3.5).
export const chaves = {
  me: ['me'] as const,
  resumo: ['targets', 'summary'] as const,
  alvos: ['targets', 'list'] as const,
  alvo: (id: string) => ['targets', id] as const,
  serie: (id: string, janela: string) => ['targets', id, 'checks', janela] as const,
  // O limite entra na chave: páginas de tamanhos diferentes não dividem o cache.
  incidentes: (filtro: string, alvo?: string, limite?: number) => ['incidents', filtro, alvo ?? '', limite ?? 0] as const,
  canais: ['channels'] as const,
  paginas: ['status-pages'] as const,
  sessoes: ['sessions'] as const,
}

// Os intervalos do poll (spec do Plano 6 §3.5). O resumo é o mais curto: é
// ele que responde "tem algo quebrado?" no painel.
const INTERVALO_DO_RESUMO_MS = 15_000
const INTERVALO_DOS_INCIDENTES_MS = 30_000
const INTERVALO_DO_ALVO_MS = 60_000
const INTERVALO_DA_SERIE_MS = 60_000
const INTERVALO_DOS_CANAIS_MS = 60_000
const INTERVALO_DOS_RESOLVIDOS_MS = 60_000

export function useMe() {
  const api = useApi()
  return useQuery({ queryKey: chaves.me, queryFn: api.me, staleTime: Infinity })
}

export function useResumo() {
  const api = useApi()
  return useQuery({ queryKey: chaves.resumo, queryFn: api.resumo, refetchInterval: INTERVALO_DO_RESUMO_MS })
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
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (ultima) => ultima.next_cursor,
    // O poll de uma infinite query refaz todas as páginas carregadas, em série,
    // e gasta o rate limit por usuário. O maxPages não serve: exige lista
    // bidirecional, e o cursor só anda para frente. Então o poll só roda com
    // uma página; com mais, a lista se atualiza no foco e no reconhecer.
    refetchInterval: (q) => ((q.state.data?.pages.length ?? 0) > 1 ? false : INTERVALO_DOS_INCIDENTES_MS),
  })
}

// Os incidentes abertos: uma página de até 100, o teto da rota. Uma consulta
// só, dividida pelo contador da navegação (spec do Plano 6 §4.9), pelos
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

// Mais que uma página vira "99+".
function contarAbertos(d: ListaDeIncidentes): string {
  return d.next_cursor ? '99+' : d.items.length > 99 ? '99+' : String(d.items.length)
}

function listarAbertos(d: ListaDeIncidentes): Abertos {
  return { itens: d.items, completa: d.next_cursor === undefined }
}

export function useAbertos() {
  return useConsultaDosAbertos(contarAbertos)
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
