import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

export const estiloDeEntrada =
  'block w-full min-h-11 min-w-0 rounded-lg border border-borda-controle bg-superficie px-3 py-2.5 text-sm text-texto aria-invalid:border-perigo'

// Campo liga rótulo, ajuda e erro ao controle: aria-describedby e
// aria-invalid, como o leitor de tela precisa. O controle vem por função,
// que recebe as props de acessibilidade.
export function Campo({
  rotulo,
  ajuda,
  erro,
  children,
}: {
  rotulo: ReactNode
  ajuda?: ReactNode
  erro?: string
  children: (a11y: { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }) => ReactNode
}) {
  const id = useId()
  const idAjuda = `${id}-ajuda`
  const idErro = `${id}-erro`
  const descrito = [ajuda ? idAjuda : null, erro ? idErro : null].filter(Boolean).join(' ') || undefined
  return (
    <div className="min-w-0 space-y-1.5">
      <label htmlFor={id} className="block text-sm font-medium">
        {rotulo}
      </label>
      {children({ id, 'aria-describedby': descrito, 'aria-invalid': erro ? true : undefined })}
      {ajuda && (
        <p id={idAjuda} className="text-sm text-suave">
          {ajuda}
        </p>
      )}
      {erro && (
        <p id={idErro} className="text-sm text-perigo">
          {erro}
        </p>
      )}
    </div>
  )
}

export function Entrada({ className, ...props }: ComponentProps<'input'>) {
  return <input className={cn(estiloDeEntrada, className)} {...props} />
}

export function Selecao({ className, ...props }: ComponentProps<'select'>) {
  return <select className={cn(estiloDeEntrada, className)} {...props} />
}

// Erro do formulário inteiro (não de um campo): anunciado assim que aparece.
export function ErroDoFormulario({ children }: { children?: ReactNode }) {
  if (!children) return null
  return (
    <p role="alert" className="rounded-md border border-perigo p-3 text-sm text-perigo">
      {children}
    </p>
  )
}
