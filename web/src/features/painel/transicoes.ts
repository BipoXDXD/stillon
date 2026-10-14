import type { ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, textoDoEstado, type EstadoExibido } from '@/lib/estado'

// O que anunciar entre duas respostas do resumo: só as MUDANÇAS de estado.
// Nada na primeira carga (anterior null) e nada num poll sem mudança — um
// leitor de tela lendo "API: no ar" a cada 15 s tornaria a região inútil.
export function transicoes(anterior: Map<string, EstadoExibido> | null, itens: ItemDoResumo[]): string[] {
  if (anterior === null) return []
  const frases: string[] = []
  for (const it of itens) {
    const antes = anterior.get(it.id)
    const agora = estadoExibido(it.state, it.enabled)
    if (antes !== undefined && antes !== agora) {
      frases.push(agora === 'up' && antes === 'down' ? `${it.name}: voltou ao ar` : `${it.name}: ${textoDoEstado(agora).toLowerCase()}`)
    }
  }
  return frases
}

export function mapaDeEstados(itens: ItemDoResumo[]): Map<string, EstadoExibido> {
  return new Map(itens.map((it) => [it.id, estadoExibido(it.state, it.enabled)]))
}
