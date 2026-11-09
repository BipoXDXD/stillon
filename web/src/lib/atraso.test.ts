import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAposUmTempo } from './atraso'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useAposUmTempo', () => {
  const montar = (ativo: boolean) => renderHook(({ a }) => useAposUmTempo(a, 300), { initialProps: { a: ativo } })

  // Fronteira: 299 e 300 ms. Sabotagem: devolver o "ativo" direto.
  it('só fica verdadeiro quando o tempo vence', () => {
    const { result } = montar(true)
    expect(result.current).toBe(false)
    act(() => {
      vi.advanceTimersByTime(299)
    })
    expect(result.current).toBe(false)
    act(() => {
      vi.advanceTimersByTime(1)
    })
    expect(result.current).toBe(true)
  })

  it('inativo não conta tempo', () => {
    const { result } = montar(false)
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(result.current).toBe(false)
  })

  it('volta a falso ao desativar, e conta do zero ao reativar', () => {
    const { result, rerender } = montar(true)
    act(() => {
      vi.advanceTimersByTime(300)
    })
    rerender({ a: false })
    expect(result.current).toBe(false)
    rerender({ a: true })
    act(() => {
      vi.advanceTimersByTime(299)
    })
    expect(result.current).toBe(false)
    act(() => {
      vi.advanceTimersByTime(1)
    })
    expect(result.current).toBe(true)
  })

  it('desativar antes do prazo cancela', () => {
    const { result, rerender } = montar(true)
    act(() => {
      vi.advanceTimersByTime(100)
    })
    rerender({ a: false })
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(result.current).toBe(false)
  })
})
