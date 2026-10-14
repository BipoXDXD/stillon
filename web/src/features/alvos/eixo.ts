import type { PontoBruto, PontoHorario } from './serie'

// Largura dos eixos do gráfico de latência a partir do rótulo mais largo. O
// jsdom não mede texto, então a conta usa a largura de cada caractere na
// Inter a 16 px (o tamanho que o <text> do Recharts herda do body), medida no
// Chromium com as cifras tabulares, arredondada para cima. O e2e
// (a11y.spec.ts, "rótulos dos eixos do gráfico inteiros") confere o resultado.
const PX_POR_CARACTERE: Readonly<Record<string, number>> = { ' ': 4, m: 15, s: 9, '%': 17 }
const PX_DA_CIFRA = 10
// O traço do tique (6 px) e a margem até o texto (2 px), do Recharts, mais
// uma reserva para a variação de renderização entre sistemas.
const PX_DO_TRACO_E_MARGEM = 8
const PX_DE_RESERVA = 6

export function larguraDoEixo(rotulo: string): number {
  const texto = Array.from(rotulo).reduce((soma, c) => soma + (PX_POR_CARACTERE[c] ?? PX_DA_CIFRA), 0)
  return Math.ceil(texto) + PX_DO_TRACO_E_MARGEM + PX_DE_RESERVA
}

// O Recharts arredonda o último tique para cima, num valor redondo: o rótulo
// do topo pode ter uma cifra a mais que o maior ponto (9800 vira 10000).
const MULTIPLOS_REDONDOS = [1, 2, 5] as const

function topoDaEscala(maior: number): number {
  if (maior <= 0) return 0
  for (let potencia = 1; ; potencia *= 10) {
    const redondo = MULTIPLOS_REDONDOS.map((m) => m * potencia).find((v) => v >= maior)
    if (redondo !== undefined) return redondo
  }
}

export function rotuloDoTopo(maiorMs: number): string {
  return `${topoDaEscala(maiorMs)} ms`
}

export function maiorLatencia(pontos: readonly (PontoBruto | PontoHorario)[]): number {
  return pontos.reduce((maior, p) => Math.max(maior, 'latencia' in p ? (p.latencia ?? 0) : (p.faixa?.[1] ?? p.p50 ?? 0)), 0)
}
