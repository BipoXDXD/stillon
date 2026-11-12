import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { Api } from './cliente'
import { useEventosDoHeartbeat, useIncidentes, useIncidentesDoHeartbeat } from './consultas'

const api = vi.hoisted(() => ({
  incidentes: vi.fn<Api['incidentes']>(),
  eventosDoHeartbeat: vi.fn<Api['eventosDoHeartbeat']>(),
  incidentesDoHeartbeat: vi.fn<Api['incidentesDoHeartbeat']>(),
}))

// Só as três chamadas que o teste observa; o resto do Api não é usado.
vi.mock('@/app/contexto', () => ({ useApi: () => api as unknown as Api }))

function envolver({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>
}

// A primeira página nunca leva cursor: o servidor trataria uma string vazia
// como um cursor inválido. Sabotagem prevista: trocar o initialPageParam
// por ''.
describe('a primeira página das listas paginadas', () => {
  it('useIncidentes pede sem cursor', async () => {
    api.incidentes.mockResolvedValue({ items: [] })
    renderHook(() => useIncidentes('open'), { wrapper: envolver })
    await waitFor(() => expect(api.incidentes).toHaveBeenCalled())
    expect(api.incidentes.mock.calls[0]?.[0]).toMatchObject({ status: 'open', cursor: undefined })
  })

  it('useEventosDoHeartbeat pede sem cursor', async () => {
    api.eventosDoHeartbeat.mockResolvedValue({ items: [] })
    renderHook(() => useEventosDoHeartbeat('h1'), { wrapper: envolver })
    await waitFor(() => expect(api.eventosDoHeartbeat).toHaveBeenCalled())
    expect(api.eventosDoHeartbeat).toHaveBeenCalledWith('h1', undefined)
  })

  it('useIncidentesDoHeartbeat pede sem cursor', async () => {
    api.incidentesDoHeartbeat.mockResolvedValue({ items: [] })
    renderHook(() => useIncidentesDoHeartbeat('h1'), { wrapper: envolver })
    await waitFor(() => expect(api.incidentesDoHeartbeat).toHaveBeenCalled())
    expect(api.incidentesDoHeartbeat).toHaveBeenCalledWith('h1', undefined)
  })
})
