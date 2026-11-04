import { situacaoDoIncidente } from '@/components/SeloDoIncidente'
import type { Incidente } from '@/lib/api/schemas'

// A tela de incidentes (spec do redesign, Telas > 3) em funções puras: os
// grupos "Em andamento" e "Resolvidos recentes" e a frase de resumo.

export type GruposDeIncidentes = { emAndamento: Incidente[]; resolvidos: Incidente[] }

// "Em andamento" é todo incidente aberto, reconhecido ou não: reconhecer
// para os lembretes, não resolve.
export function agruparIncidentes(itens: readonly Incidente[]): GruposDeIncidentes {
  return {
    emAndamento: itens.filter((i) => i.resolved_at === null),
    resolvidos: itens.filter((i) => i.resolved_at !== null),
  }
}

// "1 em andamento, 2 reconhecidos." sobre os abertos (a consulta do
// contador da navegação). Sem abertos, nada. Página cheia: o servidor manda
// next_cursor sempre que ela vem cheia, então pode haver exatamente 100.
export function resumoDosAbertos(abertos: readonly Incidente[], completa: boolean): string | null {
  if (abertos.length === 0) return null
  if (!completa) return `${abertos.length} ou mais incidentes abertos.`
  const reconhecidos = abertos.filter((i) => situacaoDoIncidente(i) === 'reconhecido').length
  const emAndamento = abertos.length - reconhecidos
  const partes = [
    emAndamento > 0 ? `${emAndamento} em andamento` : null,
    reconhecidos > 0 ? `${reconhecidos} ${reconhecidos === 1 ? 'reconhecido' : 'reconhecidos'}` : null,
  ].filter((p): p is string => p !== null)
  return `${partes.join(', ')}.`
}
