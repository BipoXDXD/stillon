import { CircleAlert, X } from 'lucide-react'
import { Toast } from 'radix-ui'
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { useSessao } from '@/app/contexto'
import { TITULO_DA_TELA } from './botao'

// Avisos passageiros ("Alvo salvo"). Radix Toast, e não o sonner: o sonner
// injeta um <style> sem suporte a nonce, que a CSP do painel bloqueia (o
// toast saía sem estilo, largura total no rodapé).

type Aviso = { id: number; texto: string; tipo: 'ok' | 'erro' }

type Avisar = (texto: string, tipo?: Aviso['tipo']) => void

const DURACAO_DO_SUCESSO_MS = 6000
const MAX_ERROS_NA_TELA = 3

// null fora do provedor: um aviso nunca some em silêncio.
const Contexto = createContext<Avisar | null>(null)

export function ProvedorDeAvisos({ children }: { children: ReactNode }) {
  const { estado } = useSessao()
  const [avisos, setAvisos] = useState<Aviso[]>([])
  const regiao = useRef<HTMLOListElement>(null)
  const devolverFoco = useRef(false)
  const avisar = useCallback((texto: string, tipo: Aviso['tipo'] = 'ok') => {
    // O mesmo texto repetido não empilha: um "Copiar" que falha a cada clique
    // cobriria a tela.
    setAvisos((a) => (a.some((x) => x.texto === texto) ? a : [...a, { id: Date.now() + Math.random(), texto, tipo }]))
  }, [])

  // O provedor fica acima do roteador: o erro de uma sessão apareceria no
  // login da seguinte. Ajuste durante o render, na mudança de estado da sessão.
  const [sessaoVista, setSessaoVista] = useState(estado)
  if (sessaoVista !== estado) {
    setSessaoVista(estado)
    if (estado === 'anonimo') setAvisos(avisos.filter((x) => x.tipo !== 'erro'))
  }

  // O Radix põe o foco no <ol> da região, mesmo vazio, e quem usa teclado
  // fica em lugar nenhum. Vai ao "X" que sobrou, ou ao título da tela. Um
  // efeito a cada render: quem o pede é o render da remoção.
  useEffect(() => {
    if (!devolverFoco.current) return
    devolverFoco.current = false
    const proximo = Array.from(regiao.current?.querySelectorAll<HTMLElement>('[data-dispensar]') ?? []).at(-1)
    const destino = proximo ?? TITULO_DA_TELA.current
    destino?.focus()
  })

  const erros = avisos.filter((a) => a.tipo === 'erro')
  const naTela = new Set(erros.slice(-MAX_ERROS_NA_TELA))
  const escondidos = erros.length - naTela.size

  return (
    <Toast.Provider swipeDirection="right" label="Aviso">
      <Contexto.Provider value={avisar}>{children}</Contexto.Provider>
      {avisos
        .filter((a) => a.tipo === 'ok' || naTela.has(a))
        .map((a) => (
          <Toast.Root
            key={a.id}
            type={a.tipo === 'erro' ? 'foreground' : 'background'}
            duration={a.tipo === 'erro' ? Infinity : DURACAO_DO_SUCESSO_MS}
            onOpenChange={(aberto) => {
              if (aberto) return
              // Antes daqui, o Radix já moveu para a região o foco que estava no aviso.
              devolverFoco.current = document.activeElement === regiao.current
              setAvisos((l) => l.filter((x) => x.id !== a.id))
            }}
            className={`rounded-padrao border bg-superficie-elevada text-sm sombra-nivel-2 ${
              a.tipo === 'erro' ? 'flex items-start gap-2 border-perigo py-1 pr-1 pl-3' : 'border-borda p-3'
            }`}
          >
            {a.tipo === 'erro' && <CircleAlert aria-hidden="true" className="mt-2 size-4 shrink-0 text-perigo" />}
            <Toast.Title className={a.tipo === 'erro' ? 'min-w-0 flex-1 py-2' : undefined}>{a.texto}</Toast.Title>
            {a.tipo === 'erro' && (
              <Toast.Close
                data-dispensar=""
                aria-label="Dispensar aviso"
                className="inline-flex size-11 shrink-0 items-center justify-center rounded-padrao text-suave hover:bg-painel"
              >
                <X aria-hidden="true" className="size-4" />
              </Toast.Close>
            )}
          </Toast.Root>
        ))}
      {/* bottom-16 no celular: o aviso fica acima da barra inferior fixa. A
          altura máxima e a rolagem mantêm todo "X" alcançável; ela desconta a
          linha do "e mais N", que fica fora da lista porque os toasts entram
          nela por portal, na ordem em que nascem. */}
      <div className="fixed right-4 bottom-16 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2 lg:bottom-4">
        <Toast.Viewport
          ref={regiao}
          label="Avisos ({hotkey})"
          className="flex max-h-[calc(100dvh-9rem)] flex-col gap-2 overflow-y-auto"
        />
        {escondidos > 0 && <p className="rounded-padrao border border-borda bg-superficie-elevada p-3 text-sm text-suave sombra-nivel-2">e mais {escondidos}</p>}
      </div>
    </Toast.Provider>
  )
}

export function useAvisar(): Avisar {
  const avisar = useContext(Contexto)
  if (!avisar) throw new Error('fora do ProvedorDeAvisos')
  return avisar
}
