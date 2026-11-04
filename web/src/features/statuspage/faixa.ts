import type { EstadoAlvo } from '@/lib/api/schemas'

// A faixa de estado da página pública (spec do redesign, Telas 6), para a
// prévia do painel. Os casos ficam em internal/httpapi/testdata/faixa.json.
// A página publicada só passa a dizer a mesma frase com o PR 7 do redesign
// (faixa em internal/httpapi/publicpage.go, cujo teste lê o mesmo JSON); até
// lá, ela diz "Há serviços fora do ar." e a prévia é mais detalhada que ela.
export type ItemDaFaixa = { readonly nome: string; readonly estado: EstadoAlvo }
export type Faixa = { readonly estado: EstadoAlvo; readonly frase: string; readonly detalhe: string }

// Os rótulos que a página pública já usa para o estado de cada serviço.
const ROTULO_PUBLICO: Record<EstadoAlvo, string> = {
  up: 'No ar',
  degraded: 'Lento',
  down: 'Fora do ar',
  unknown: 'Sem dados',
}

// Do pior ao melhor: o primeiro presente dá a frase.
const GRAVIDADE = ['down', 'degraded', 'unknown'] as const

const FRASES: Record<(typeof GRAVIDADE)[number], (quantos: string) => string> = {
  down: (quantos) => `${quantos} fora do ar.`,
  degraded: (quantos) => `Instabilidade em ${quantos}.`,
  unknown: (quantos) => `Sem dados recentes de ${quantos}.`,
}

function servicos(n: number): string {
  return n === 1 ? '1 serviço' : `${n} serviços`
}

export function faixaDaPagina(itens: readonly ItemDaFaixa[]): Faixa {
  if (itens.length === 0) return { estado: 'unknown', frase: 'Nenhum serviço nesta página.', detalhe: '' }
  const grupos = GRAVIDADE.map((estado) => ({
    estado,
    nomes: itens.filter((i) => i.estado === estado).map((i) => i.nome),
  })).filter((g) => g.nomes.length > 0)
  const pior = grupos[0]
  if (!pior) return { estado: 'up', frase: 'Todos os sistemas funcionando.', detalhe: '' }
  return {
    estado: pior.estado,
    frase: FRASES[pior.estado](servicos(pior.nomes.length)),
    detalhe: grupos.map((g) => `${ROTULO_PUBLICO[g.estado]}: ${g.nomes.join(', ')}.`).join(' '),
  }
}
