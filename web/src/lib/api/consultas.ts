import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useApi } from '@/app/contexto'

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

export function useMe() {
  const api = useApi()
  return useQuery({ queryKey: chaves.me, queryFn: api.me, staleTime: Infinity })
}

export function useResumo() {
  const api = useApi()
  return useQuery({ queryKey: chaves.resumo, queryFn: api.resumo, refetchInterval: 15_000 })
}

export function useAlvos() {
  const api = useApi()
  return useQuery({ queryKey: chaves.alvos, queryFn: api.alvos })
}

export function useAlvo(id: string) {
  const api = useApi()
  return useQuery({ queryKey: chaves.alvo(id), queryFn: () => api.alvo(id), refetchInterval: 60_000 })
}

export function useSerie(id: string, janela: string, desde: () => string) {
  const api = useApi()
  return useQuery({
    queryKey: chaves.serie(id, janela),
    queryFn: () => api.serie(id, desde()),
    refetchInterval: 60_000,
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
    refetchInterval: 30_000,
  })
}

// O contador de incidentes abertos da navegação (spec do Plano 6 §4.9): uma
// página de até 100 (o teto da rota); mais que isso vira "99+". A chave fica
// sob ['incidents'], e o reconhecer, que invalida ['incidents'], o atualiza.
export function useAbertos() {
  const api = useApi()
  return useQuery({
    queryKey: [...chaves.incidentes('open', undefined, 100), 'contagem'],
    queryFn: () => api.incidentes({ status: 'open', limit: 100 }),
    refetchInterval: 30_000,
    select: (d) => (d.next_cursor ? '99+' : d.items.length > 99 ? '99+' : String(d.items.length)),
  })
}

export function useCanais() {
  const api = useApi()
  return useQuery({ queryKey: chaves.canais, queryFn: api.canais, refetchInterval: 60_000 })
}

export function usePaginas() {
  const api = useApi()
  return useQuery({ queryKey: chaves.paginas, queryFn: api.paginas })
}

export function useSessoes() {
  const api = useApi()
  return useQuery({ queryKey: chaves.sessoes, queryFn: api.sessoes })
}
