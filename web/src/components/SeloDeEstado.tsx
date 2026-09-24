import { Circle, CircleDashed, Pause, Triangle, X } from 'lucide-react'
import type { EstadoAlvo } from '@/lib/api/schemas'
import { cn } from '@/lib/utils'

// Estado com ícone E texto, nunca só cor (spec §11.6): verde e vermelho são
// o par que a deuteranopia não separa. As formas também diferem.
export type EstadoExibido = EstadoAlvo | 'pausado'

const selos: Record<EstadoExibido, { texto: string; Icone: typeof Circle; cor: string; cheio: boolean }> = {
  up: { texto: 'No ar', Icone: Circle, cor: 'text-no-ar', cheio: true },
  degraded: { texto: 'Degradado', Icone: Triangle, cor: 'text-degradado', cheio: true },
  down: { texto: 'Fora do ar', Icone: X, cor: 'text-fora', cheio: false },
  unknown: { texto: 'Sem dados', Icone: CircleDashed, cor: 'text-sem-dados', cheio: false },
  pausado: { texto: 'Pausado', Icone: Pause, cor: 'text-sem-dados', cheio: false },
}

export function textoDoEstado(e: EstadoExibido) {
  return selos[e].texto
}

// O servidor diz "unknown" também para alvo desativado; o painel distingue
// pelo enabled (spec do Plano 6 §4.2).
export function estadoExibido(estado: EstadoAlvo, ativo: boolean): EstadoExibido {
  return ativo ? estado : 'pausado'
}

export function SeloDeEstado({ estado, className }: { estado: EstadoExibido; className?: string }) {
  const { texto, Icone, cor, cheio } = selos[estado]
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-sm font-medium', cor, className)}>
      <Icone aria-hidden="true" className="size-4" fill={cheio ? 'currentColor' : 'none'} strokeWidth={2.5} />
      {texto}
    </span>
  )
}
