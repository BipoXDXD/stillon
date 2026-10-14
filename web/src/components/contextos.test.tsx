import { renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useAnunciar } from '@/components/Anunciador'
import { useAvisar } from '@/components/ui/avisos'
import { useTema } from '@/lib/tema'

// G5 da revisão de 2026-10-14: o contexto nascia com um valor padrão que não
// fazia nada (avisar, anunciar e mudar o tema em silêncio). Um componente
// montado fora do provedor perdia o aviso sem erro nenhum. Agora o contexto
// nasce null e o hook lança, como o useApi. Sabotagem: voltar o default
// no-op de qualquer um → o caso dele não lança.
describe('hooks de contexto fora do provedor', () => {
  it.each([
    ['useAvisar', useAvisar, 'fora do ProvedorDeAvisos'],
    ['useAnunciar', useAnunciar, 'fora do ProvedorDeAnuncios'],
    ['useTema', useTema, 'fora do ProvedorDeTema'],
  ])('%s lança', (_nome, hook, mensagem) => {
    // O React registra o erro de render no console; aqui ele é o esperado.
    const silenciar = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      expect(() => renderHook(() => hook())).toThrow(mensagem)
    } finally {
      silenciar.mockRestore()
    }
  })
})
