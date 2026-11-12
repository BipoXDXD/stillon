import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { ProvedorDoPainel } from '@/app/contexto'
import { novaSessao } from '@/test/ambiente'
import { useEventosDoHeartbeat, useIncidentes, useIncidentesDoHeartbeat } from './consultas'

// Monta os hooks sobre a Api de verdade (criarApi), com um buscar que só grava
// a URL pedida e devolve uma página vazia.
function montar() {
  const urls: string[] = []
  const { sessao, api } = novaSessao({
    buscar: async (entrada) => {
      urls.push(entrada)
      return Response.json({ items: [] })
    },
  })
  const envolver = ({ children }: { children: ReactNode }) => (
    <ProvedorDoPainel sessao={sessao} api={api}>
      {children}
    </ProvedorDoPainel>
  )
  return { urls, envolver }
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
