import { useHeartbeats } from '@/lib/api/consultas'
import type { Heartbeat } from '@/lib/api/schemas'
import { pedeAtencao } from './estado'

// A regra única da tela de incidentes e do contador da aba.
export function useTarefasQuePedemAtencao(): Heartbeat[] {
  return useHeartbeats().data?.items.filter(pedeAtencao) ?? []
}
