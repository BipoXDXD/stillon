import { cva, type VariantProps } from 'class-variance-authority'
import { Slot } from 'radix-ui'
import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

const variantes = cva(
  'inline-flex items-center justify-center gap-2 rounded-md text-sm font-bold whitespace-nowrap disabled:pointer-events-none disabled:opacity-60 aria-disabled:opacity-60',
  {
    variants: {
      tipo: {
        primario: 'bg-primaria text-primaria-texto hover:opacity-90',
        secundario: 'border border-borda bg-superficie text-texto hover:bg-painel',
        perigo: 'border border-perigo bg-superficie text-perigo hover:bg-painel',
        fantasma: 'text-texto hover:bg-painel',
      },
      tamanho: { normal: 'min-h-11 px-4 py-2', pequeno: 'min-h-10 px-3 py-1.5' },
    },
    defaultVariants: { tipo: 'primario', tamanho: 'normal' },
  },
)

type Props = ComponentProps<'button'> & VariantProps<typeof variantes> & { asChild?: boolean }

export function Botao({ className, tipo, tamanho, asChild = false, type, ...props }: Props) {
  const Comp = asChild ? Slot.Root : 'button'
  return (
    <Comp
      className={cn(variantes({ tipo, tamanho }), className)}
      type={asChild ? undefined : (type ?? 'button')}
      {...props}
    />
  )
}
