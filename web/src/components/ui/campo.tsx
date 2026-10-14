import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// 44 px de altura (alvo de toque) e 16 px de fonte no celular: abaixo disso o
// Safari do iPhone amplia a página ao focar o campo. A borda é a de controle
// (3:1); o foco é o anel índigo de 3 px do :focus-visible global.
export const estiloDeEntrada =
  'block min-h-11 w-full min-w-0 rounded-padrao border border-borda-controle bg-superficie px-3 py-2 text-base text-texto placeholder:text-apagado aria-invalid:border-perigo sm:text-sm'

export type AcessibilidadeDoCampo = { id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean }

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
  children: (a11y: AcessibilidadeDoCampo) => ReactNode
}) {
  const id = useId()
  const idAjuda = `${id}-ajuda`
  const idErro = `${id}-erro`
  const descrito = [ajuda ? idAjuda : null, erro ? idErro : null].filter(Boolean).join(' ') || undefined
  return (
    <div className="min-w-0 space-y-2">
      <label htmlFor={id} className="block text-sm font-semibold">
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

// Checkbox com o rótulo na mesma linha, que inteira é a área de toque (44 px).
// A cor da marca vem do accent-color global do index.css (DI 12). className
// vai para a linha; o resto das props, ao input (o register do
// react-hook-form passa name, ref e eventos).
export function CaixaDeMarcar({
  children,
  className,
  ...props
}: Omit<ComponentProps<'input'>, 'type' | 'children'> & { children: ReactNode }) {
  return (
    <label className={cn('flex min-h-11 items-center gap-3 text-sm', className)}>
      <input type="checkbox" className="size-5 shrink-0" {...props} />
      {children}
    </label>
  )
}

// Erro do formulário inteiro (não de um campo): anunciado assim que aparece.
export function ErroDoFormulario({ children }: { children?: ReactNode }) {
  if (!children) return null
  return (
    <p role="alert" className="rounded-padrao border border-perigo bg-fora-100 p-3 text-sm text-perigo">
      {children}
    </p>
  )
}
