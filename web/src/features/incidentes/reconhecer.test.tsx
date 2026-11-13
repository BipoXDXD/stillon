import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { useReconhecer } from './reconhecer'

function montar(aoTerminar: (id: string) => void) {
  const qc = new QueryClient()
  const enviar = vi.fn<(id: string) => Promise<unknown>>(() => Promise.resolve())
  const envolver = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  )
  const { result } = renderHook(
    () =>
      useReconhecer({
        enviar,
        consulta: ['incidentes'],
        aoNaoEncontrar: () => {},
        aoErrar: () => {},
        aoTerminar,
      }),
    { wrapper: envolver },
  )
  return { result, enviar }
}

describe('useReconhecer', () => {
  it('libera o incidente mesmo quando o aoTerminar lança', async () => {
    const { result, enviar } = montar(() => {
      throw new Error('foco falhou')
    })

    await act(() => expect(result.current.reconhecer('i1')).rejects.toThrow('foco falhou'))
    await act(() => expect(result.current.reconhecer('i1')).rejects.toThrow('foco falhou'))

    expect(enviar).toHaveBeenCalledTimes(2)
    expect(result.current.reconhecendo.has('i1')).toBe(false)
  })
})
