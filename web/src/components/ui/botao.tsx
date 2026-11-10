import { cva } from 'class-variance-authority'
import { Slot } from 'radix-ui'
import { useLayoutEffect, useRef, type ComponentProps, type RefObject } from 'react'
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

// A reserva de quem não tem um elemento melhor por perto: o <h1> da tela ou,
// se ele só vem com os dados e eles ainda não chegaram, o <main>.
export const TITULO_DA_TELA: RefObject<HTMLElement | null> = {
  get current() {
    return document.querySelector<HTMLElement>('main h1') ?? document.querySelector<HTMLElement>('main')
  },
}

// Um botão que some com o próprio clique, ou quando os dados mudam: o foco,
// que estava nele, iria para o <body>. Se ele tinha o foco, o foco vai à
// reserva. O efeito de layout roda na desmontagem ANTES de o React tirar o
// <button> do DOM, com a ref ainda presa a ele. A reserva só é lida depois do
// commit, porque ela pode nascer nele (o <h1> que vem com os dados).
export function BotaoQueSome({ reserva, ...props }: PropsDoBotao & { reserva: RefObject<HTMLElement | null> }) {
  const eu = useRef<HTMLButtonElement>(null)
  useLayoutEffect(() => {
    const botao = eu.current
    return () => {
      // A reserva é lida depois de propósito: é o elemento que está na tela
      // quando o commit termina.
      // oxlint-disable-next-line react-hooks/exhaustive-deps
      if (botao && document.activeElement === botao) queueMicrotask(() => reserva.current?.focus())
    }
  }, [reserva])
  return <Botao ref={eu} {...props} />
}
