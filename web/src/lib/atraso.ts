import { useEffect, useState } from 'react'

// Abaixo disto a espera mal se percebe, e um sinal de carregamento só piscaria.
export const ATRASO_DO_SINAL_MS = 300

// Verdadeiro depois de `ativo` passar `atrasoMs` seguidos verdadeiro.
export function useAposUmTempo(ativo: boolean, atrasoMs: number): boolean {
  const [venceu, setVenceu] = useState(false)
  useEffect(() => {
    if (!ativo) return
    const relogio = setTimeout(() => setVenceu(true), atrasoMs)
    return () => {
      clearTimeout(relogio)
      setVenceu(false)
    }
  }, [ativo, atrasoMs])
  return ativo && venceu
}
