import { Circle, CircleDashed, Pause, Triangle, X, type LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { cn } from '@/lib/utils'

// Estado com forma, cor E texto, nunca só cor (spec §11.6 e spec do
// redesign): verde e vermelho são o par que a deuteranopia não separa, então
// os ícones também diferem.
export type TomDoSelo = 'no-ar' | 'degradado' | 'fora' | 'sem-dados' | 'primaria'

const selos: Record<EstadoExibido, { Icone: LucideIcon; tom: TomDoSelo; cheio: boolean }> = {
  up: { Icone: Circle, tom: 'no-ar', cheio: true },
  degraded: { Icone: Triangle, tom: 'degradado', cheio: true },
  down: { Icone: X, tom: 'fora', cheio: false },
  unknown: { Icone: CircleDashed, tom: 'sem-dados', cheio: false },
  pausado: { Icone: Pause, tom: 'sem-dados', cheio: false },
}

export function SeloDeEstado({ estado, className }: { estado: EstadoExibido; className?: string }) {
  const { Icone, tom, cheio } = selos[estado]
  return (
    <Selo tom={tom} Icone={Icone} cheio={cheio} className={className}>
      {textoDoEstado(estado)}
    </Selo>
  )
}

// Pílula com o fundo 100 do tom e o texto na cor dele (todo par tem 4,5:1,
// src/tokens.test.ts). O tom primaria é o do selo de reconhecido.
const TONS: Record<TomDoSelo, string> = {
  'no-ar': 'bg-no-ar-100 text-no-ar',
  degradado: 'bg-degradado-100 text-degradado',
  fora: 'bg-fora-100 text-fora',
  'sem-dados': 'bg-sem-dados-100 text-sem-dados',
  primaria: 'bg-primaria-100 text-primaria',
}

export type PropsDoSelo = { tom: TomDoSelo; Icone: LucideIcon; cheio?: boolean; children: ReactNode; className?: string }

export function Selo({ tom, Icone, cheio = false, children, className }: PropsDoSelo) {
  return (
    <span className={cn('inline-flex items-center gap-1 rounded-full px-2 py-1 text-meta font-semibold whitespace-nowrap', TONS[tom], className)}>
      <Icone aria-hidden="true" className="size-3.5 shrink-0" fill={cheio ? 'currentColor' : 'none'} strokeWidth={2.5} />
      {children}
    </span>
  )
}
