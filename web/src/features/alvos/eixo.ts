// Largura do eixo Y do gráfico de latência a partir do rótulo mais largo, o
// do teto (escala.ts). O jsdom não mede texto, então a conta usa a largura de
// cada caractere na Inter a 16 px (o tamanho que o <text> do Recharts herda
// do body), medida no Chromium com as cifras tabulares, arredondada para
// cima. O e2e (a11y.spec.ts, "rótulos dos eixos do gráfico inteiros")
// confere o resultado.
const PX_POR_CARACTERE: Readonly<Record<string, number>> = { ' ': 4, m: 15, s: 9 }
const PX_DA_CIFRA = 10
// O traço do tique (6 px) e a margem até o texto (2 px), do Recharts, mais
// uma reserva para a variação de renderização entre sistemas.
const PX_DO_TRACO_E_MARGEM = 8
const PX_DE_RESERVA = 6

export function larguraDoEixo(rotulo: string): number {
  const texto = Array.from(rotulo).reduce((soma, c) => soma + (PX_POR_CARACTERE[c] ?? PX_DA_CIFRA), 0)
  return Math.ceil(texto) + PX_DO_TRACO_E_MARGEM + PX_DE_RESERVA
}
