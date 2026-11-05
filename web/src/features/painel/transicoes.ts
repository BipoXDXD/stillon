import { estadoDoHeartbeat, textoDoHeartbeat } from '@/features/heartbeats/estado'
import type { Heartbeat, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, textoDoEstado, type EstadoExibido } from '@/lib/estado'

// O que anunciar entre duas respostas: só as MUDANÇAS de estado. Nada na
// primeira carga (anterior null) e nada num poll sem mudança — um leitor de
// tela lendo "API: no ar" a cada 15 s tornaria a região inútil.
type Anunciavel = { id: string; nome: string; estado: EstadoExibido; texto: string; tarefa: boolean }

export function anunciaveis(itens: readonly ItemDoResumo[], heartbeats: readonly Heartbeat[]): Anunciavel[] {
  const alvos = itens.map((it) => {
    const estado = estadoExibido(it.state, it.enabled)
    return { id: it.id, nome: it.name, estado, texto: textoDoEstado(estado), tarefa: false }
  })
  const tarefas = heartbeats.map((hb) => ({
    id: hb.id,
    nome: hb.name,
    estado: estadoDoHeartbeat(hb.state),
    texto: textoDoHeartbeat(hb.state),
    tarefa: true,
  }))
  return [...alvos, ...tarefas]
}

type Anterior = Pick<Anunciavel, 'estado' | 'texto'>

// Compara o texto: atrasado e falhando têm o mesmo estado exibido, e a troca
// de um pelo outro é notícia. Uma tarefa não "volta ao ar" (spec da tela, T2).
export function transicoes(anterior: Map<string, Anterior> | null, atuais: readonly Anunciavel[]): string[] {
  if (anterior === null) return []
  const frases: string[] = []
  for (const m of atuais) {
    const antes = anterior.get(m.id)
    if (antes === undefined || antes.texto === m.texto) continue
    const voltou = !m.tarefa && m.estado === 'up' && antes.estado === 'down'
    frases.push(voltou ? `${m.nome}: voltou ao ar` : `${m.nome}: ${m.texto.toLowerCase()}`)
  }
  return frases
}

export function mapaDeEstados(atuais: readonly Anunciavel[]): Map<string, Anterior> {
  return new Map(atuais.map((m) => [m.id, { estado: m.estado, texto: m.texto }]))
}
