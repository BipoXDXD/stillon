import { Toast } from 'radix-ui'
import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'

// Avisos passageiros ("Alvo salvo"). Radix Toast, e não o sonner: o sonner
// injeta um <style> sem suporte a nonce, que a CSP do painel bloqueia (o
// toast saía sem estilo, largura total no rodapé — spike, §8.1).

type Aviso = { id: number; texto: string; tipo: 'ok' | 'erro' }

type Avisar = (texto: string, tipo?: Aviso['tipo']) => void

// null fora do provedor: um aviso nunca some em silêncio.
const Contexto = createContext<Avisar | null>(null)

export function ProvedorDeAvisos({ children }: { children: ReactNode }) {
  const [avisos, setAvisos] = useState<Aviso[]>([])
  const avisar = useCallback((texto: string, tipo: Aviso['tipo'] = 'ok') => {
    setAvisos((a) => [...a, { id: Date.now() + Math.random(), texto, tipo }])
  }, [])
  return (
    <Toast.Provider swipeDirection="right" label="Aviso">
      <Contexto.Provider value={avisar}>{children}</Contexto.Provider>
      {avisos.map((a) => (
        <Toast.Root
          key={a.id}
          type={a.tipo === 'erro' ? 'foreground' : 'background'}
          duration={6000}
          onOpenChange={(aberto) => {
            if (!aberto) setAvisos((l) => l.filter((x) => x.id !== a.id))
          }}
          className={`rounded-xl border bg-superficie p-3 text-sm shadow ${a.tipo === 'erro' ? 'border-perigo' : 'border-borda'}`}
        >
          <Toast.Title>{a.texto}</Toast.Title>
        </Toast.Root>
      ))}
      <Toast.Viewport className="fixed right-4 bottom-4 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" />
    </Toast.Provider>
  )
}

export function useAvisar(): Avisar {
  const avisar = useContext(Contexto)
  if (!avisar) throw new Error('fora do ProvedorDeAvisos')
  return avisar
}
