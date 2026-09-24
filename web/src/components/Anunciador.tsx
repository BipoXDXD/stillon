import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'

// A região aria-live do painel (spec §11.6): uma só, no layout. Quem muda de
// estado anuncia aqui; o leitor de tela lê sem o foco sair do lugar.
const Contexto = createContext<(texto: string) => void>(() => {})

export function ProvedorDeAnuncios({ children }: { children: ReactNode }) {
  const [texto, setTexto] = useState('')
  const anunciar = useCallback((t: string) => {
    // Esvaziar antes garante que o mesmo texto, anunciado duas vezes, seja
    // lido duas vezes.
    setTexto('')
    requestAnimationFrame(() => setTexto(t))
  }, [])
  return (
    <Contexto.Provider value={anunciar}>
      {children}
      <output aria-live="polite" aria-atomic="true" className="sr-only" data-testid="anuncios">
        {texto}
      </output>
    </Contexto.Provider>
  )
}

export function useAnunciar() {
  return useContext(Contexto)
}
