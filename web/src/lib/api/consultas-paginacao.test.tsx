import { act, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { criarClienteDeConsultas, ProvedorDoPainel } from '@/app/contexto'
import { novaSessao } from '@/test/ambiente'
import { chaves, useAlvos, useEventosDoHeartbeat, useIncidentes, useIncidentesDoHeartbeat } from './consultas'
import type { Buscar } from './http'

// Monta os hooks sobre a Api de verdade (criarApi), com um buscar que só grava
// a URL pedida e devolve uma página vazia.
function montar(buscar?: Buscar) {
  const urls: string[] = []
  const cliente = criarClienteDeConsultas()
  const { sessao, api } = novaSessao({
    buscar: buscar ?? (async (entrada) => {
      urls.push(entrada)
      return Response.json({ items: [] })
    }),
  })
  const envolver = ({ children }: { children: ReactNode }) => (
    <ProvedorDoPainel sessao={sessao} api={api} cliente={cliente}>
      {children}
    </ProvedorDoPainel>
  )
  return { urls, envolver, cliente }
}

// A primeira página sai sem cursor na URL; o cursor só aparece a partir da
// segunda, vindo do next_cursor da anterior.
describe('a primeira página das listas paginadas', () => {
  it('useIncidentes pede sem cursor', async () => {
    const { urls, envolver } = montar()
    renderHook(() => useIncidentes('open'), { wrapper: envolver })
    await waitFor(() => expect(urls).toHaveLength(1))
    expect(urls[0]).toBe('/api/v1/incidents?status=open')
  })

  it('useEventosDoHeartbeat pede sem cursor', async () => {
    const { urls, envolver } = montar()
    renderHook(() => useEventosDoHeartbeat('h1'), { wrapper: envolver })
    await waitFor(() => expect(urls).toHaveLength(1))
    expect(urls[0]).toBe('/api/v1/heartbeats/h1/events?limit=20')
  })

  it('useIncidentesDoHeartbeat pede sem cursor', async () => {
    const { urls, envolver } = montar()
    renderHook(() => useIncidentesDoHeartbeat('h1'), { wrapper: envolver })
    await waitFor(() => expect(urls).toHaveLength(1))
    expect(urls[0]).toBe('/api/v1/heartbeats/h1/incidents?limit=20')
  })
})

type ConsultaObservada = { data: unknown; error: Error | null; pedir: () => Promise<unknown> }

it.each([
  {
    nome: 'lista simples',
    chave: chaves.alvos,
    url: '/api/v1/targets?limit=100',
    useConsulta(): ConsultaObservada {
      const consulta = useAlvos()
      return { data: consulta.data, error: consulta.error, pedir: consulta.refetch }
    },
  },
  {
    nome: 'próxima página com cursor',
    chave: chaves.eventosDoHeartbeat('h1'),
    url: '/api/v1/heartbeats/h1/events?cursor=p2&limit=20',
    useConsulta(): ConsultaObservada {
      const consulta = useEventosDoHeartbeat('h1')
      return { data: consulta.data, error: consulta.error, pedir: consulta.fetchNextPage }
    },
  },
])('cancelQueries aborta o fetch da $nome e preserva o cache', async ({ chave, url, useConsulta }) => {
  const buscar = vi.fn<Buscar>(
    (_entrada, { signal }) =>
      new Promise<Response>((_, rejeitar) => {
        signal?.addEventListener('abort', () => rejeitar(signal.reason), { once: true })
      }),
  )
  buscar.mockResolvedValueOnce(Response.json({ items: [], next_cursor: 'p2' }))
  const { cliente, envolver } = montar(buscar)
  const { result } = renderHook(useConsulta, { wrapper: envolver })
  await waitFor(() => expect(result.current.data).toBeDefined())
  const cacheAnterior = result.current.data

  act(() => {
    void result.current.pedir()
  })
  await waitFor(() => expect(buscar).toHaveBeenCalledTimes(2))
  const [rota, init] = buscar.mock.calls[1] ?? []
  expect(rota).toBe(url)
  await act(() => cliente.cancelQueries({ queryKey: chave, exact: true }))

  expect(init?.signal?.aborted).toBe(true)
  expect(buscar).toHaveBeenCalledTimes(2)
  expect(cliente.getQueryData(chave)).toEqual(cacheAnterior)
  expect(result.current.error).toBeNull()
})
