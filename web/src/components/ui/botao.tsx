import { cva } from 'class-variance-authority'
import { Slot } from 'radix-ui'
import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

// Pirâmide de botões (DI 4, spec do redesign): uma ação primária sólida por
// tela, secundárias com contorno, terciárias só em texto. O vermelho sólido
// (perigo) é só do botão que confirma uma ação destrutiva num diálogo; antes
// da confirmação, Apagar, Encerrar e Desativar são terciários
// (src/sistema.test.ts conta os usos). Todo tamanho tem 44 px de altura
// (alvo de toque, WCAG 2.5.5).
export type TipoDeBotao = 'primario' | 'secundario' | 'terciario' | 'perigo'
export type TamanhoDeBotao = 'normal' | 'pequeno' | 'icone'

const variantes = cva(
  'inline-flex min-h-11 items-center justify-center gap-2 rounded-padrao font-semibold whitespace-nowrap disabled:pointer-events-none disabled:opacity-60 aria-disabled:opacity-60',
  {
    variants: {
      tipo: {
        primario: 'bg-primaria text-primaria-texto hover:bg-primaria/90',
        secundario: 'border border-borda-controle bg-superficie text-texto hover:bg-painel',
        terciario: 'text-primaria hover:bg-primaria-100',
        perigo: 'bg-perigo text-perigo-texto hover:bg-perigo/90',
      } satisfies Record<TipoDeBotao, string>,
      tamanho: {
        normal: 'px-4 text-sm',
        pequeno: 'px-3 text-meta',
        icone: 'size-11 p-0',
      } satisfies Record<TamanhoDeBotao, string>,
    },
    defaultVariants: { tipo: 'primario', tamanho: 'normal' },
  },
)

export type PropsDoBotao = ComponentProps<'button'> & { tipo?: TipoDeBotao; tamanho?: TamanhoDeBotao; asChild?: boolean }

export function Botao({ className, tipo, tamanho, asChild = false, type, ...props }: PropsDoBotao) {
  const Comp = asChild ? Slot.Root : 'button'
  return (
    <Comp
      className={cn(variantes({ tipo, tamanho }), className)}
      type={asChild ? undefined : (type ?? 'button')}
      {...props}
    />
  )
}
