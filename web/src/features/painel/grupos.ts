import { textoDaCausa } from '@/lib/api/mensagens'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'
import { duracao, haQuantoAteAgora, milissegundos } from '@/lib/format'

// A ordem do painel: fora do ar, degradado, no ar, sem dados e pausado.
// Funções puras: a tela só as chama.
const GRAVIDADE: Record<EstadoExibido, number> = { down: 0, degraded: 1, up: 2, unknown: 3, pausado: 4 }

// numeric: "Serviço 2" antes de "Serviço 10"; base: maiúscula e acento não
// separam. A ordem do servidor (id decrescente) não ajuda a achar um alvo
// entre 30.
const PELO_NOME = new Intl.Collator('pt-BR', { sensitivity: 'base', numeric: true })

export function compararNomes(a: string, b: string): number {
  return PELO_NOME.compare(a, b)
}

export function ehProblema(e: EstadoExibido): boolean {
  return e === 'down' || e === 'degraded'
}

export function ordenarPorGravidade<T extends { estado: EstadoExibido; nome: string }>(linhas: readonly T[]): T[] {
  return [...linhas].sort((a, b) => GRAVIDADE[a.estado] - GRAVIDADE[b.estado] || compararNomes(a.nome, b.nome))
}

// As três zonas da lista (RUI, densidade): o problema pede atenção e tem
// mais espaço; quem está no ar e quem está parado ficam rebaixados.
export type Zona = 'problema' | 'no-ar' | 'parado'

export function zonaDoEstado(e: EstadoExibido): Zona {
  if (ehProblema(e)) return 'problema'
  return e === 'up' ? 'no-ar' : 'parado'
}

export const TEXTO_DA_ZONA: Record<Zona, string> = {
  problema: 'Com problema',
  'no-ar': 'No ar',
  parado: 'Sem dados ou pausados',
}

export type BlocoDaLista<T> = { zona: Zona; linhas: T[] }

// Corta a lista, na ordem dada, em blocos seguidos da mesma zona. Na ordem
// por gravidade, um bloco por zona; não reordena nada.
export function separarEmZonas<T extends { estado: EstadoExibido }>(linhas: readonly T[]): BlocoDaLista<T>[] {
  const blocos: BlocoDaLista<T>[] = []
  for (const linha of linhas) {
    const zona = zonaDoEstado(linha.estado)
    const bloco = blocos.at(-1)
    if (bloco?.zona === zona) bloco.linhas.push(linha)
    else blocos.push({ zona, linhas: [linha] })
  }
  return blocos
}

// A situação de um problema: com incidente aberto, a causa e
// há quanto tempo; sem ele, a latência (degradado) ou o último check (fora
// do ar antes de o incidente abrir).
export function detalheDoProblema(it: ItemDoResumo, incidente: Incidente | undefined, agora: Date): string {
  if (incidente) {
    return `${textoDaCausa(incidente.cause)} · há ${duracao(agora.getTime() - Date.parse(incidente.started_at))}`
  }
  if (it.state === 'degraded') {
    return it.last_latency_ms === null ? 'Lento no último check' : `Latência de ${milissegundos(it.last_latency_ms)} no último check`
  }
  return it.last_checked_at === null ? 'Nenhum check ainda' : `Último check ${haQuantoAteAgora(it.last_checked_at, agora)}`
}

// Uma linha de texto por alvo, sem gráfico.
export function linhaSemDados(it: ItemDoResumo, agora: Date): string {
  if (!it.enabled) return 'Pausado'
  return it.last_checked_at === null
    ? 'Sem dados · nunca verificado'
    : `Sem dados · último check ${haQuantoAteAgora(it.last_checked_at, agora)}`
}
