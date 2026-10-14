import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'

// Tema: Sistema (prefers-color-scheme), Claro ou Escuro, persistido. É a
// única coisa que o painel guarda no navegador, e não é segredo (a spec §5.3
// proíbe o storage para tokens). /tema.js aplica o mesmo antes da primeira
// pintura, com a mesma chave.
export type Tema = 'sistema' | 'claro' | 'escuro'

const CHAVE = 'stillon.tema'
const ESCURO = '(prefers-color-scheme: dark)'

export function ehTema(v: string | null): v is Tema {
  return v === 'sistema' || v === 'claro' || v === 'escuro'
}

function lerTema(): Tema {
  try {
    const v = localStorage.getItem(CHAVE)
    return ehTema(v) ? v : 'sistema'
  } catch {
    return 'sistema'
  }
}

function aplicar(t: Tema) {
  const escuro = t === 'escuro' || (t === 'sistema' && window.matchMedia(ESCURO).matches)
  document.documentElement.classList.toggle('dark', escuro)
}

type ValorDoTema = { tema: Tema; mudar: (t: Tema) => void }

// null fora do provedor: trocar o tema nunca é ignorado em silêncio.
const Contexto = createContext<ValorDoTema | null>(null)

export function ProvedorDeTema({ children }: { children: ReactNode }) {
  const [tema, setTema] = useState<Tema>(lerTema)

  useEffect(() => {
    aplicar(tema)
    if (tema !== 'sistema') return
    const mq = window.matchMedia(ESCURO)
    const ouvir = () => aplicar('sistema')
    mq.addEventListener('change', ouvir)
    return () => mq.removeEventListener('change', ouvir)
  }, [tema])

  const mudar = useCallback((t: Tema) => {
    try {
      localStorage.setItem(CHAVE, t)
    } catch {
      // Sem storage (modo privado restrito), o tema vale só nesta aba.
    }
    setTema(t)
  }, [])

  return <Contexto.Provider value={{ tema, mudar }}>{children}</Contexto.Provider>
}

export function useTema(): ValorDoTema {
  const valor = useContext(Contexto)
  if (!valor) throw new Error('fora do ProvedorDeTema')
  return valor
}
