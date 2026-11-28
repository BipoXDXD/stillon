import { keepPreviousData, onlineManager, useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { useSyncExternalStore } from 'react'
import { useApi, useSessao } from '@/app/contexto'
import { useApiDoProjeto, useProjeto } from '@/app/projeto'
import type { FiltroDoResumo } from './cliente'
import type { Incidente, ItemDoResumo, ListaDeIncidentes } from './schemas'

const MS_DA_PREVIA = 60_000

// As chaves e os intervalos de atualização (spec §11.4).
export const chaves = {
  me: ['me'] as const,
  // GET /auth/signup-config: pública, a mesma com e sem sessão.
  configDoCadastro: ['signup-config'] as const,
  sessoes: ['sessions'] as const,
  // A lista de projetos é da conta, e fica fora de qualquer projeto.
  projetos: ['projects'] as const,
  // As chaves de recurso de um projeto, todas sob ['projeto', id]: o cache de
  // um projeto nunca serve outro, e invalidar `todas` alcança o projeto
  // inteiro. Dentro dele, os prefixos: invalidar um deles alcança tudo que
  // está abaixo (o resumo filtrado fica abaixo do resumo).
  doProjeto: (id: string) => {
    const p = ['projeto', id] as const
    return {
      todas: p,
      previaDoCron: (expr: string, timezone: string) => [...p, 'previa-do-cron', expr, timezone] as const,
      todosOsAlvos: [...p, 'targets'] as const,
      todosOsIncidentes: [...p, 'incidents'] as const,
      resumo: [...p, 'targets', 'summary'] as const,
      resumoFiltrado: (f: FiltroDoResumo) => [...p, 'targets', 'summary', f.tags, f.estados] as const,
      alvos: [...p, 'targets', 'list'] as const,
      alvo: (alvo: string) => [...p, 'targets', alvo] as const,
      serie: (alvo: string, janela: string) => [...p, 'targets', alvo, 'checks', janela] as const,
      incidentes: (filtro: string, alvo?: string, limite?: number) =>
        [...p, 'incidents', filtro, alvo ?? '', limite ?? 0] as const,
      heartbeats: [...p, 'heartbeats'] as const,
      heartbeat: (hb: string) => [...p, 'heartbeats', hb] as const,
      eventosDoHeartbeat: (hb: string) => [...p, 'heartbeats', hb, 'events'] as const,
      incidentesDoHeartbeat: (hb: string) => [...p, 'heartbeats', hb, 'incidents'] as const,
      canais: [...p, 'channels'] as const,
      paginas: [...p, 'status-pages'] as const,
    }
  },
}

// Os intervalos do poll. O resumo é o mais curto: é
// ele que responde "tem algo quebrado?" no painel.
const INTERVALO_DO_RESUMO_MS = 15_000
const INTERVALO_DOS_INCIDENTES_MS = 30_000
const INTERVALO_DO_ALVO_MS = 60_000
const INTERVALO_DA_SERIE_MS = 60_000
const INTERVALO_DOS_CANAIS_MS = 60_000
const INTERVALO_DOS_RESOLVIDOS_MS = 60_000

// A primeira página de uma lista paginada não leva cursor. A anotação alarga o
// tipo para string | undefined, o que o getNextPageParam devolve.
const PRIMEIRA_PAGINA: string | undefined = undefined

const estaSemRede = () => !onlineManager.isOnline()

// Sem rede o React Query pausa as consultas em vez de falhar: nenhum erro
// nasce, e quem só olha a consulta acha que o que está na tela é atual.
export function useSemRede(): boolean {
  return useSyncExternalStore(onlineManager.subscribe, estaSemRede)
}

// As chaves do projeto atual, para quem invalida depois de uma mutação.
export function useChavesDoProjeto() {
  return chaves.doProjeto(useProjeto().id)
}

// A API e as chaves do projeto atual: toda consulta de recurso sai por aqui.
function useDoProjeto() {
  return { api: useApiDoProjeto(), chave: useChavesDoProjeto() }
}

export function useProjetos() {
  const api = useApi()
  return useQuery({ queryKey: chaves.projetos, queryFn: ({ signal }) => api.projetos(signal) })
}

export function useMe() {
  const api = useApi()
  return useQuery({ queryKey: chaves.me, queryFn: ({ signal }) => api.me(signal), staleTime: Infinity })
}

// Se o cadastro público está ligado, e a site key do Turnstile. Só depois do
// boot da sessão: quando ele termina anônimo, o cache inteiro é limpo
// (LimparAoEncerrar), e uma config buscada antes sumiria com a tela aberta,
// levando junto o formulário e o widget.
export function useConfigDoCadastro() {
  const api = useApi()
  const { estado } = useSessao()
  return useQuery({
    queryKey: chaves.configDoCadastro,
    queryFn: ({ signal }) => api.configDoCadastro(signal),
    enabled: estado !== 'verificando',
  })
}

export function useResumo() {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.resumo,
    queryFn: ({ signal }) => api.resumo(undefined, signal),
    refetchInterval: INTERVALO_DO_RESUMO_MS,
  })
}

// A tabela com filtro de tag ou estado. A consulta completa continua em poll:
// a manchete, as contagens e a lista de tags falam do conjunto inteiro. Até a
// resposta chegar, fica o que estava na tela (spec do painel denso, Estados).
export function useResumoFiltrado(filtro: FiltroDoResumo, ativo: boolean, naTela: { items: ItemDoResumo[] }) {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.resumoFiltrado(filtro),
    queryFn: ({ signal }) => api.resumo(filtro, signal),
    refetchInterval: INTERVALO_DO_RESUMO_MS,
    enabled: ativo,
    placeholderData: (anterior) => anterior ?? naTela,
  })
}

export function useAlvos() {
  const { api, chave } = useDoProjeto()
  return useQuery({ queryKey: chave.alvos, queryFn: ({ signal }) => api.alvos(signal) })
}

export function useAlvo(id: string) {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.alvo(id),
    queryFn: ({ signal }) => api.alvo(id, signal),
    refetchInterval: INTERVALO_DO_ALVO_MS,
  })
}

export function useSerie(id: string, janela: string) {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.serie(id, janela),
    queryFn: ({ signal }) => api.serie(id, janela, signal),
    refetchInterval: INTERVALO_DA_SERIE_MS,
    placeholderData: keepPreviousData,
  })
}

export function useIncidentes(filtro: 'open' | 'resolved' | 'all', alvo?: string, limite?: number) {
  const { api, chave } = useDoProjeto()
  return useInfiniteQuery({
    queryKey: chave.incidentes(filtro, alvo, limite),
    queryFn: ({ pageParam, signal }) =>
      api.incidentes(
        {
          status: filtro === 'all' ? undefined : filtro,
          cursor: pageParam,
          targetId: alvo,
          limit: limite,
        },
        signal,
      ),
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
// precisa pelo select. A chave fica sob os incidentes do projeto, e o
// reconhecer, que invalida esse prefixo, atualiza todos.
const LIMITE_DOS_ABERTOS = 100

export type Abertos = { itens: Incidente[]; completa: boolean }

function useConsultaDosAbertos<T>(select: (d: ListaDeIncidentes) => T) {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: [...chave.incidentes('open', undefined, LIMITE_DOS_ABERTOS), 'abertos'],
    queryFn: ({ signal }) => api.incidentes({ status: 'open', limit: LIMITE_DOS_ABERTOS }, signal),
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
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: [...chave.incidentes('resolved', undefined, LIMITE_DOS_RESOLVIDOS), 'ultimo'],
    queryFn: ({ signal }) => api.incidentes({ status: 'resolved', limit: LIMITE_DOS_RESOLVIDOS }, signal),
    enabled: ativa,
    refetchInterval: INTERVALO_DOS_RESOLVIDOS_MS,
  })
}

// No ritmo do resumo: a manchete fala dos dois (spec da tela, A manchete).
export function useHeartbeats() {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.heartbeats,
    queryFn: ({ signal }) => api.heartbeats(signal),
    refetchInterval: INTERVALO_DO_RESUMO_MS,
  })
}

export function useHeartbeat(id: string) {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.heartbeat(id),
    queryFn: ({ signal }) => api.heartbeat(id, signal),
    refetchInterval: INTERVALO_DO_RESUMO_MS,
  })
}

// Sem retry: 400 é a resposta para quem ainda está digitando. A expressão não
// muda de resposta na escala de um minuto, e o cache poupa o vaivém do campo.
export function usePreviaDoCron(expr: string, timezone: string) {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.previaDoCron(expr, timezone),
    queryFn: ({ signal }) => api.previaDoCron(expr, timezone, signal),
    enabled: expr !== '' && timezone !== '',
    retry: false,
    staleTime: MS_DA_PREVIA,
  })
}

export function useEventosDoHeartbeat(id: string) {
  const { api, chave } = useDoProjeto()
  return useInfiniteQuery({
    queryKey: chave.eventosDoHeartbeat(id),
    queryFn: ({ pageParam, signal }) => api.eventosDoHeartbeat(id, pageParam, signal),
    initialPageParam: PRIMEIRA_PAGINA,
    getNextPageParam: (ultima) => ultima.next_cursor,
    refetchInterval: pollSoComUmaPagina,
  })
}

export function useIncidentesDoHeartbeat(id: string) {
  const { api, chave } = useDoProjeto()
  return useInfiniteQuery({
    queryKey: chave.incidentesDoHeartbeat(id),
    queryFn: ({ pageParam, signal }) => api.incidentesDoHeartbeat(id, pageParam, signal),
    initialPageParam: PRIMEIRA_PAGINA,
    getNextPageParam: (ultima) => ultima.next_cursor,
    refetchInterval: pollSoComUmaPagina,
  })
}

export function useCanais() {
  const { api, chave } = useDoProjeto()
  return useQuery({
    queryKey: chave.canais,
    queryFn: ({ signal }) => api.canais(signal),
    refetchInterval: INTERVALO_DOS_CANAIS_MS,
  })
}

export function usePaginas() {
  const { api, chave } = useDoProjeto()
  return useQuery({ queryKey: chave.paginas, queryFn: ({ signal }) => api.paginas(signal) })
}

export function useSessoes() {
  const api = useApi()
  return useQuery({ queryKey: chaves.sessoes, queryFn: ({ signal }) => api.sessoes(signal) })
}
