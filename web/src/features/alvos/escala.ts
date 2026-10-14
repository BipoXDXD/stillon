import { diaMes, horaMinuto, milissegundos } from '@/lib/format'
import type { Intervalo, PontoDeLatencia } from './serie'

// A escala do gráfico de latência (spec do redesign, Telas > 2). Funções
// puras: o Recharts não desenha marcas de eixo no jsdom, e o que decide o
// desenho (o teto, o pico fora da escala, o domínio, as marcas) é testado
// aqui.

export type EscalaY = { teto: number; pico: PontoDeLatencia | null; foraDaEscala: number }
export type EixoX = { marcas: number[]; porDia: boolean }

// O teto vem do p95 dos pontos com 25% de folga: um pico isolado não esmaga
// a série. Se nada passa disso, a escala vai até o maior ponto, com 10% de
// ar acima dele.
const FOLGA_DO_P95 = 1.25
const AR_ACIMA_DO_MAIOR = 1.1
const TETO_MINIMO_MS = 10
const DEGRAUS = [1, 2, 2.5, 5, 10]

export function escalaRobusta(pontos: readonly PontoDeLatencia[]): EscalaY {
  if (pontos.length === 0) return { teto: TETO_MINIMO_MS, pico: null, foraDaEscala: 0 }
  const ordenados = pontos.map((p) => p.ms).sort((a, b) => a - b)
  // Posto mais próximo, como o resumir() da série.
  const p95 = ordenados[Math.max(0, Math.ceil(0.95 * ordenados.length) - 1)] ?? 0
  const maior = ordenados.at(-1) ?? 0
  const limite = p95 * FOLGA_DO_P95
  const teto = arredondarParaCima(maior <= limite ? maior * AR_ACIMA_DO_MAIOR : limite)
  const acima = pontos.filter((p) => p.ms > teto)
  const pico = acima.reduce<PontoDeLatencia | null>((m, p) => (m === null || p.ms > m.ms ? p : m), null)
  return { teto, pico, foraDaEscala: acima.length }
}

// O menor número redondo (1, 2, 2,5 ou 5 vezes uma potência de 10) que
// cobre x: as marcas do eixo Y saem limpas.
function arredondarParaCima(x: number): number {
  if (x <= TETO_MINIMO_MS) return TETO_MINIMO_MS
  const potencia = 10 ** Math.floor(Math.log10(x))
  return (DEGRAUS.find((d) => d * potencia >= x) ?? 10) * potencia
}

// As marcas do eixo Y de 0 ao teto, em passos redondos: com o teto como
// última marca, o maior rótulo do eixo é conhecido, e a largura do eixo
// (eixo.ts) sai dele. Cinco intervalos, ou quatro quando o quinto do teto não
// é redondo (200 / 5 = 40; 200 / 4 = 50).
export function marcasDoEixoY(teto: number): number[] {
  const intervalos = ehRedondo(teto / 5) ? 5 : 4
  return Array.from({ length: intervalos + 1 }, (_, i) => (teto * i) / intervalos)
}

function ehRedondo(x: number): boolean {
  const potencia = 10 ** Math.floor(Math.log10(x))
  return DEGRAUS.some((d) => Math.abs(d * potencia - x) < 1e-9 * x)
}

// A anotação da legenda: "pico de 2.400 ms às 03:10, fora da escala".
export function textoDoPico(escala: EscalaY, comData: boolean): string | null {
  const { pico, foraDaEscala } = escala
  if (pico === null) return null
  const quando = comData ? `em ${diaMes(pico.t)}, ${horaMinuto(pico.t)}` : `às ${horaMinuto(pico.t)}`
  const valor = `${milissegundos(pico.ms)} ${quando}`
  return foraDaEscala === 1 ? `pico de ${valor}, fora da escala` : `${foraDaEscala} pontos fora da escala; o pico, ${valor}`
}

// Abertura mínima de 1 h: uma série de um ponto só fica no meio de uma
// janela com marcas, e não num eixo de largura zero.
const ABERTURA_MINIMA_MS = 3_600_000

export function dominioX(tempos: readonly number[], faixas: readonly Intervalo[]): [number, number] | null {
  if (tempos.length === 0) return null
  const de = Math.min(...tempos, ...faixas.map((f) => f.de))
  const ate = Math.max(...tempos, ...faixas.map((f) => f.ate))
  if (ate - de >= ABERTURA_MINIMA_MS) return [de, ate]
  const meio = (de + ate) / 2
  return [meio - ABERTURA_MINIMA_MS / 2, meio + ABERTURA_MINIMA_MS / 2]
}

const MAXIMO_DE_MARCAS = 6
const PASSOS_EM_MINUTOS = [15, 30, 60, 120, 180, 360, 720]
const PASSOS_EM_DIAS = [1, 2, 7, 14]

// Marcas em horas redondas no fuso do navegador (quartos e meias horas na
// janela de 1 h; meias-noites nas de dias): o menor passo que dá até seis.
// As datas saem do construtor local, e não de soma de milissegundos, para
// uma troca de horário de verão não deslocar as marcas.
export function ticksDoEixoX(de: number, ate: number): EixoX {
  for (const minutos of PASSOS_EM_MINUTOS) {
    const marcas = marcasDesdeAMeiaNoite(de, ate, (d, k) => new Date(d.getFullYear(), d.getMonth(), d.getDate(), 0, k * minutos))
    if (marcas.length <= MAXIMO_DE_MARCAS) return { marcas, porDia: false }
  }
  let marcas: number[] = []
  for (const dias of PASSOS_EM_DIAS) {
    marcas = marcasDesdeAMeiaNoite(de, ate, (d, k) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + k * dias))
    if (marcas.length <= MAXIMO_DE_MARCAS) break
  }
  return { marcas, porDia: true }
}

// Para no primeiro passo que passa do teto: quem chama só quer saber se
// cabem até seis.
function marcasDesdeAMeiaNoite(de: number, ate: number, marca: (meiaNoite: Date, k: number) => Date): number[] {
  const inicio = new Date(de)
  const meiaNoite = new Date(inicio.getFullYear(), inicio.getMonth(), inicio.getDate())
  const marcas: number[] = []
  for (let k = 0; marcas.length <= MAXIMO_DE_MARCAS; k++) {
    const t = marca(meiaNoite, k).getTime()
    if (t > ate) break
    if (t >= de) marcas.push(t)
  }
  return marcas
}
