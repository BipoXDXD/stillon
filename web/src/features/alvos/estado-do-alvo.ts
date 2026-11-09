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

// As quatro formas de um KPI. O número grande e a unidade à parte (spec: Inter
// 600 de 28 px, unidade em 13 px); sem número, a forma diz por quê: ainda não
// veio, a consulta falhou, ou a resposta veio e não há o que medir.
export type ValorDoIndicador =
  | { forma: 'valor'; valor: string; unidade: string }
  | { forma: 'carregando' }
  | { forma: 'indisponivel' }
  | { forma: 'sem-dados'; motivo: string }

const semDados = (motivo: string): ValorDoIndicador => ({ forma: 'sem-dados', motivo })

// Pelo porcento(), que trunca: "100,00" nunca esconde uma falha.
export function emPorcento(fracao: number | null, motivo: string): ValorDoIndicador {
  return fracao === null ? semDados(motivo) : { forma: 'valor', valor: porcento(fracao).slice(0, -1), unidade: '%' }
}

export function emMilissegundos(ms: number | null, motivo: string): ValorDoIndicador {
  return ms === null ? semDados(motivo) : { forma: 'valor', valor: Math.round(ms).toLocaleString('pt-BR'), unidade: 'ms' }
}

// Sem o item, o painel não sabe: "nenhum check" é só para o alvo que está no
// resumo e ainda não teve check.
export function ultimoCheck(item: Pick<ItemDoResumo, 'last_checked_at'> | undefined, agora: Date): ValorDoIndicador {
  if (item === undefined) return semDados('Aguardando o resumo')
  const iso = item.last_checked_at
  return iso === null ? semDados('Nenhum check ainda') : { forma: 'valor', valor: horaMinuto(iso), unidade: haQuanto(iso, agora) }
}

// isLoading é a primeira busca em andamento. Sem dados e sem busca, a consulta
// falhou ou está pausada sem rede: em nenhum dos dois o esqueleto terminaria.
export function indicadorDaConsulta<T>(
  consulta: { data: T | undefined; isLoading: boolean },
  ler: (dados: T) => ValorDoIndicador,
): ValorDoIndicador {
  if (consulta.data !== undefined) return ler(consulta.data)
  return consulta.isLoading ? { forma: 'carregando' } : { forma: 'indisponivel' }
}

// Um aviso só para as três consultas do topo. O resumo vem primeiro: é ele que
// diz se o alvo está no ar. Com dado guardado, a faixa continua afirmando um
// estado, e o aviso diz de quando ele é.
export function textoDoAviso(resumo: { data: unknown; isError: boolean; dataUpdatedAt: number }, seriesFalharam: boolean): string | null {
  if (resumo.isError) {
    if (resumo.data === undefined) return 'Não foi possível saber o estado deste alvo.'
    return `Não foi possível atualizar; mostrando dados de ${horaMinuto(new Date(resumo.dataUpdatedAt).toISOString())}.`
  }
  return seriesFalharam ? 'Não foi possível atualizar todos os indicadores.' : null
}
