import { textoDaCausa } from '@/lib/api/mensagens'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { dataHora, duracao, haQuanto, horaMinuto, milissegundos, porcento } from '@/lib/format'

// O topo do detalhe (spec do redesign, Telas > 2) em funções puras: a faixa
// de estado e os valores dos quatro indicadores.

export type Faixa = { estado: EstadoExibido; titulo: string; detalhe: string }

// O título diz o estado e, com incidente aberto, há quanto tempo; o
// detalhe, a causa e o início, ou o que se sabe do último check. Pausado
// vence: os checks pararam, e o incidente em aberto não muda isso.
export function faixaDoEstado(item: ItemDoResumo, aberto: Incidente | null, agora: Date): Faixa {
  const estado = estadoExibido(item.state, item.enabled)
  if (estado === 'pausado') {
    return { estado, titulo: 'Pausado', detalhe: 'Os checks estão parados. Retome para voltar a vigiar.' }
  }
  const nome = textoDoEstado(estado)
  if (aberto) {
    return {
      estado,
      titulo: `${nome} há ${duracao(agora.getTime() - Date.parse(aberto.started_at))}`,
      detalhe: `${textoDaCausa(aberto.cause)} · desde ${dataHora(aberto.started_at)}`,
    }
  }
  if (estado === 'degraded' && item.last_latency_ms !== null) {
    return { estado, titulo: nome, detalhe: `Latência de ${milissegundos(item.last_latency_ms)} no último check.` }
  }
  return {
    estado,
    titulo: nome,
    detalhe: item.last_checked_at === null ? 'Nenhum check ainda.' : `Último check ${haQuanto(item.last_checked_at, agora)}.`,
  }
}

// O número grande e a unidade à parte (spec: Inter 600 de 28 px, unidade
// em 13 px).
export type ValorDoIndicador = { valor: string; unidade: string }

const SEM_VALOR: ValorDoIndicador = { valor: '—', unidade: '' }

// Pelo porcento(), que trunca: "100,00" nunca esconde uma falha.
export function emPorcento(fracao: number | null): ValorDoIndicador {
  return fracao === null ? SEM_VALOR : { valor: porcento(fracao).slice(0, -1), unidade: '%' }
}

export function emMilissegundos(ms: number | null): ValorDoIndicador {
  return ms === null ? SEM_VALOR : { valor: Math.round(ms).toLocaleString('pt-BR'), unidade: 'ms' }
}

export function ultimoCheck(iso: string | null, agora: Date): ValorDoIndicador {
  return iso === null ? { valor: '—', unidade: 'nunca' } : { valor: horaMinuto(iso), unidade: haQuanto(iso, agora) }
}
