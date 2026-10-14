import { useRef, type KeyboardEvent } from 'react'
import { cn } from '@/lib/utils'

// Controle segmentado (spec do redesign, Componentes base): a janela do
// gráfico e o filtro de incidentes. Segue o padrão "radio group" do WAI-ARIA
// APG: só o item marcado fica na ordem do Tab, as setas andam em volta, Home
// e End vão às pontas, e mover o foco também escolhe. Implementação própria,
// e não o RadioGroup do Radix: no Radix, a escolha depende de a seta ainda
// estar pressionada quando o foco chega (num setTimeout), o que no jsdom não
// acontece.
export type OpcaoSegmentada<V extends string> = { readonly valor: V; readonly rotulo: string }

export type PropsDoControleSegmentado<V extends string> = {
  rotulo: string
  opcoes: readonly OpcaoSegmentada<V>[]
  valor: V
  aoMudar: (valor: V) => void
  className?: string
}

const DESTINO: Partial<Record<string, (atual: number, total: number) => number>> = {
  ArrowRight: (atual, total) => (atual + 1) % total,
  ArrowDown: (atual, total) => (atual + 1) % total,
  ArrowLeft: (atual, total) => (atual - 1 + total) % total,
  ArrowUp: (atual, total) => (atual - 1 + total) % total,
  Home: () => 0,
  End: (_atual, total) => total - 1,
}

export function ControleSegmentado<V extends string>({ rotulo, opcoes, valor, aoMudar, className }: PropsDoControleSegmentado<V>) {
  const itens = useRef<(HTMLButtonElement | null)[]>([])
  const marcado = opcoes.findIndex((o) => o.valor === valor)
  // Valor fora das opções (uma URL editada à mão): nada marcado, e o primeiro
  // item fica na ordem do Tab para o grupo continuar alcançável.
  const naOrdemDoTab = marcado === -1 ? 0 : marcado

  function escolherPeloTeclado(e: KeyboardEvent<HTMLButtonElement>, atual: number) {
    const destino = DESTINO[e.key]?.(atual, opcoes.length)
    const opcao = destino === undefined ? undefined : opcoes[destino]
    if (destino === undefined || !opcao) return
    e.preventDefault()
    itens.current[destino]?.focus()
    if (opcao.valor !== valor) aoMudar(opcao.valor)
  }

  return (
    <div role="radiogroup" aria-label={rotulo} className={cn('inline-flex flex-wrap gap-1 rounded-padrao bg-painel p-1', className)}>
      {opcoes.map((o, i) => {
        const ativo = i === marcado
        return (
          <button
            key={o.valor}
            ref={(el) => {
              itens.current[i] = el
            }}
            type="button"
            // O padrão "radio group" do APG com botões: o roving tabindex e a
            // escolha pelas setas ficam explícitos aqui, sem o <input
            // type="radio"> escondido atrás de um <label> estilizado.
            // oxlint-disable-next-line jsx-a11y/prefer-tag-over-role
            role="radio"
            aria-checked={ativo}
            tabIndex={i === naOrdemDoTab ? 0 : -1}
            onClick={() => {
              if (!ativo) aoMudar(o.valor)
            }}
            onKeyDown={(e) => escolherPeloTeclado(e, i)}
            className={cn(
              'min-h-11 min-w-11 rounded-padrao px-3 text-sm text-suave hover:text-texto',
              ativo && 'bg-superficie font-semibold text-texto sombra-nivel-1',
            )}
          >
            {o.rotulo}
          </button>
        )
      })}
    </div>
  )
}
