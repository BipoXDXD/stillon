import { compararNomes, ehProblema, ordenarPorGravidade } from './grupos'
import { latenciaDaLinha, type LinhaDeMonitor } from './linhas'

// A ordem da tabela mora na URL, como os filtros, mas não é filtro: não esconde
// linha nenhuma, não conta para "Limpar filtros" e sobrevive a ele. A manchete
// fica de fora: ela fala do conjunto inteiro, em qualquer ordem.
export const ORDENS = ['gravidade', 'nome', 'menor-uptime', 'maior-latencia'] as const

export type Ordem = (typeof ORDENS)[number]

export const ORDEM_PADRAO: Ordem = 'gravidade'

export const TEXTO_DA_ORDEM: Record<Ordem, string> = {
  gravidade: 'Gravidade',
  nome: 'Nome',
  'menor-uptime': 'Menor tempo no ar',
  'maior-latencia': 'Maior latência',
}

const PARAMETRO = 'ordem'

// A URL é entrada externa: valor desconhecido abre na ordem padrão.
export function lerOrdem(p: URLSearchParams): Ordem {
  return ORDENS.find((o) => o === p.get(PARAMETRO)) ?? ORDEM_PADRAO
}

// A ordem padrão não vai à URL: um link sem ordem é o painel de sempre.
export function comOrdem(p: URLSearchParams, ordem: Ordem): URLSearchParams {
  const saida = new URLSearchParams(p)
  if (ordem === ORDEM_PADRAO) saida.delete(PARAMETRO)
  else saida.set(PARAMETRO, ordem)
  return saida
}

type Comparador = (a: LinhaDeMonitor, b: LinhaDeMonitor) => number

// As faixas de quem é comparado por uma medida. A falta dela não diz a mesma coisa
// para todos: o serviço fora do ar fica sem latência justamente por estar fora, e
// a tarefa atrasada não tem uptime. Eles vêm antes de quem tem a medida, para uma
// ordem nunca enterrar um incêndio. Sem medida e sem problema, vai para o fim.
const SEM_MEDIDA_COM_PROBLEMA = 0
const COM_MEDIDA = 1
const SEM_MEDIDA = 2

function faixa(linha: LinhaDeMonitor, valor: number | null): number {
  if (valor !== null) return COM_MEDIDA
  return ehProblema(linha.estado) ? SEM_MEDIDA_COM_PROBLEMA : SEM_MEDIDA
}

function porMedida(medida: (l: LinhaDeMonitor) => number | null, sentido: 1 | -1): Comparador {
  return (a, b) => {
    const x = medida(a)
    const y = medida(b)
    if (x === null || y === null) return faixa(a, x) - faixa(b, y)
    return (x - y) * sentido
  }
}

const COMPARADORES: Record<Exclude<Ordem, 'gravidade'>, Comparador> = {
  nome: (a, b) => compararNomes(a.nome, b.nome),
  'menor-uptime': porMedida((l) => l.uptime24h, 1),
  'maior-latencia': porMedida(latenciaDaLinha, -1),
}

// A gravidade é a base: o sort é estável, e o empate fica com o mais grave.
export function ordenarLinhas(linhas: readonly LinhaDeMonitor[], ordem: Ordem): LinhaDeMonitor[] {
  const base = ordenarPorGravidade(linhas)
  return ordem === 'gravidade' ? base : base.sort(COMPARADORES[ordem])
}
