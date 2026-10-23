import { Check, CircleAlert, CircleCheck, type LucideIcon } from 'lucide-react'
import type { Incidente } from '@/lib/api/schemas'
import { dataHora, duracao } from '@/lib/format'
import { Selo, type TomDoSelo } from './SeloDeEstado'

// O incidente na tela: a situação (em andamento, reconhecido, resolvido), o
// período e o selo (spec do redesign, Telas > 2 e 3). O detalhe do alvo e a
// tela de incidentes usam os três.
export type SituacaoDoIncidente = 'em-andamento' | 'reconhecido' | 'resolvido'

export function situacaoDoIncidente(i: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>): SituacaoDoIncidente {
  if (i.resolved_at !== null) return 'resolvido'
  return i.acknowledged_at === null ? 'em-andamento' : 'reconhecido'
}

// "durou 30 min" quando resolvido; "há 1 h 0 min" quando aberto.
export function duracaoDoIncidente(i: Pick<Incidente, 'started_at' | 'resolved_at'>, agora: Date): string {
  const inicio = Date.parse(i.started_at)
  return i.resolved_at ? `durou ${duracao(Date.parse(i.resolved_at) - inicio)}` : `há ${duracao(agora.getTime() - inicio)}`
}

export function periodoDoIncidente(i: Pick<Incidente, 'started_at' | 'resolved_at'>, agora: Date): string {
  return `início ${dataHora(i.started_at)} · ${duracaoDoIncidente(i, agora)}`
}

// Forma + cor + texto, como o selo de estado.
const SELOS: Record<SituacaoDoIncidente, { texto: string; tom: TomDoSelo; Icone: LucideIcon }> = {
  'em-andamento': { texto: 'Em andamento', tom: 'fora', Icone: CircleAlert },
  reconhecido: { texto: 'Reconhecido', tom: 'primaria', Icone: Check },
  resolvido: { texto: 'Resolvido', tom: 'sem-dados', Icone: CircleCheck },
}

export function SeloDoIncidente({
  incidente,
  className,
}: {
  incidente: Pick<Incidente, 'resolved_at' | 'acknowledged_at'>
  className?: string
}) {
  const { texto, tom, Icone } = SELOS[situacaoDoIncidente(incidente)]
  return (
    <Selo tom={tom} Icone={Icone} className={className}>
      {texto}
    </Selo>
  )
}
