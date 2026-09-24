import { AlertDialog, Dialog } from 'radix-ui'
import { useLayoutEffect, useRef, type ReactNode } from 'react'
import { Botao } from './botao'

// Dialog e AlertDialog do Radix. Os dois travam a rolagem da página com o
// react-remove-scroll, que injeta um <style>: é o nonce de src/lib/nonce.ts
// que o deixa passar pela CSP (spec do Plano 6, §8.1).

const sobreposicao = 'fixed inset-0 bg-black/50'
const caixa =
  'fixed top-1/2 left-1/2 w-[min(32rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 space-y-4 rounded-lg border border-borda bg-fundo p-6 shadow-lg max-h-[90vh] overflow-y-auto'

// O foco volta a quem abriu. O Radix devolve o foco ao seu Trigger, mas aqui
// o diálogo abre por `aberto`, sem Trigger: sem isto, o foco caía no <body>
// ao fechar (Cancelar, Esc, erro). O elemento focado é guardado quando
// `aberto` vira true, num efeito de layout: roda antes do FocusScope do
// Radix (um useEffect do conteúdo) tirar o foco de lá.
function useFocoDeVolta(aberto: boolean) {
  const gatilho = useRef<HTMLElement | null>(null)
  useLayoutEffect(() => {
    if (aberto && document.activeElement instanceof HTMLElement && document.activeElement !== document.body) {
      gatilho.current = document.activeElement
    }
  }, [aberto])
  return (e: Event) => {
    const alvo = gatilho.current
    if (!alvo?.isConnected) return
    e.preventDefault()
    alvo.focus()
  }
}

// travado: Esc e o clique fora não fecham. Para o passo que exige uma ação
// explícita antes de sair (os códigos de recuperação, que não aparecem de
// novo).
export function Dialogo({
  aberto,
  aoMudar,
  titulo,
  descricao,
  travado = false,
  children,
}: {
  aberto: boolean
  aoMudar: (v: boolean) => void
  titulo: string
  descricao?: string
  travado?: boolean
  children: ReactNode
}) {
  const segurar = (e: Event) => {
    if (travado) e.preventDefault()
  }
  const devolverFoco = useFocoDeVolta(aberto)
  return (
    <Dialog.Root open={aberto} onOpenChange={aoMudar}>
      <Dialog.Portal>
        <Dialog.Overlay className={sobreposicao} />
        <Dialog.Content
          className={caixa}
          onEscapeKeyDown={segurar}
          onPointerDownOutside={segurar}
          onCloseAutoFocus={devolverFoco}
        >
          <Dialog.Title className="text-lg font-semibold">{titulo}</Dialog.Title>
          {descricao ? (
            <Dialog.Description className="text-sm text-suave">{descricao}</Dialog.Description>
          ) : (
            <Dialog.Description className="sr-only">{titulo}</Dialog.Description>
          )}
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

// Confirmação de ação destrutiva. O foco começa em "Cancelar" (padrão do
// AlertDialog), e Esc cancela. Com `ocupado` (a ação em curso), nada fecha o
// diálogo: Cancelar fica desabilitado e Esc é ignorado.
export function Confirmar({
  aberto,
  aoMudar,
  titulo,
  descricao,
  acao,
  aoConfirmar,
  ocupado,
}: {
  aberto: boolean
  aoMudar: (v: boolean) => void
  titulo: string
  descricao: ReactNode
  acao: string
  aoConfirmar: () => void
  ocupado?: boolean
}) {
  const devolverFoco = useFocoDeVolta(aberto)
  return (
    <AlertDialog.Root
      open={aberto}
      onOpenChange={(v) => {
        if (v || !ocupado) aoMudar(v)
      }}
    >
      <AlertDialog.Portal>
        <AlertDialog.Overlay className={sobreposicao} />
        <AlertDialog.Content className={caixa} onCloseAutoFocus={devolverFoco}>
          <AlertDialog.Title className="text-lg font-semibold">{titulo}</AlertDialog.Title>
          <AlertDialog.Description className="text-sm text-suave">{descricao}</AlertDialog.Description>
          <div className="flex justify-end gap-2">
            <AlertDialog.Cancel asChild>
              <Botao tipo="secundario" disabled={ocupado}>
                Cancelar
              </Botao>
            </AlertDialog.Cancel>
            <Botao
              tipo="perigo"
              disabled={ocupado}
              onClick={(e) => {
                e.preventDefault()
                aoConfirmar()
              }}
            >
              {acao}
            </Botao>
          </div>
        </AlertDialog.Content>
      </AlertDialog.Portal>
    </AlertDialog.Root>
  )
}
