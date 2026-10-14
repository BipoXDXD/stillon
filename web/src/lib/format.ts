// Formatação para a tela, em pt-BR. Funções puras: o "agora" é parâmetro.

// Porcentagem TRUNCADA em duas casas, nunca arredondada: "100,00%" num dia
// com falha é pior que "99,99%" (D16 do Plano 4; nota do Plano 5 §12).
// O + 1e-9 absorve o erro de ponto flutuante de valores exatos (0.0029 *
// 10000 é 28,999…96, e um floor seco daria "0,28%"); é pequeno demais para
// levar 0,99995 a "100,00%".
export function porcento(fracao: number): string {
  const v = Math.floor(fracao * 10000 + 1e-9) / 100
  return `${v.toFixed(2).replace('.', ',')}%`
}

export function milissegundos(ms: number | null | undefined): string {
  return ms === null || ms === undefined ? '—' : `${Math.round(ms).toLocaleString('pt-BR')} ms`
}

export function duracao(ms: number): string {
  const min = Math.round(ms / 60_000)
  if (min < 1) return 'menos de 1 min'
  if (min < 60) return `${min} min`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} h ${min % 60} min`
  return `${Math.floor(h / 24)} d ${h % 24} h`
}

const relativa = new Intl.RelativeTimeFormat('pt-BR', { numeric: 'auto' })

const MS_POR_SEGUNDO = 1000
const SEGUNDOS_POR_MINUTO = 60
const MINUTOS_POR_HORA = 60
const HORAS_POR_DIA = 24

// A unidade é escolhida depois de arredondar, para que 59 min 50 s vire
// "há 1 hora", e não "há 60 minutos".
export function haQuanto(iso: string, agora: Date): string {
  const ms = new Date(iso).getTime() - agora.getTime()
  const seg = arredondaSimetrico(ms / MS_POR_SEGUNDO)
  if (Math.abs(seg) < SEGUNDOS_POR_MINUTO) return relativa.format(seg, 'second')
  const min = arredondaSimetrico(ms / (MS_POR_SEGUNDO * SEGUNDOS_POR_MINUTO))
  if (Math.abs(min) < MINUTOS_POR_HORA) return relativa.format(min, 'minute')
  const horas = arredondaSimetrico(ms / (MS_POR_SEGUNDO * SEGUNDOS_POR_MINUTO * MINUTOS_POR_HORA))
  if (Math.abs(horas) < HORAS_POR_DIA) return relativa.format(horas, 'hour')
  const dias = arredondaSimetrico(ms / (MS_POR_SEGUNDO * SEGUNDOS_POR_MINUTO * MINUTOS_POR_HORA * HORAS_POR_DIA))
  return relativa.format(dias, 'day')
}

// Math.round(-59.5) é -59: passado e futuro arredondariam diferente.
function arredondaSimetrico(x: number): number {
  return Math.sign(x) * Math.round(Math.abs(x))
}

const dataHoraFmt = new Intl.DateTimeFormat('pt-BR', { dateStyle: 'short', timeStyle: 'short' })

// Datas no fuso do navegador (o painel tem JavaScript, ao contrário da página
// pública, que é UTC); o fuso aparece escrito onde a hora importa.
export function dataHora(iso: string): string {
  return dataHoraFmt.format(new Date(iso))
}

export function fuso(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}

// Tamanho em code points, como o servidor conta (Plano 5, D1): "é" é 1,
// um emoji fora do plano básico também é 1; .length contaria unidades UTF-16.
export function pontosDeCodigo(s: string): number {
  return [...s].length
}

// "Firefox em Linux", a partir de um user agent. O texto é de terceiros e é
// só exibido como texto.
export function navegadorDe(ua: string): string {
  const nav = /Edg\//.test(ua)
    ? 'Edge'
    : /Firefox\//.test(ua)
      ? 'Firefox'
      : /Chrome\//.test(ua)
        ? 'Chrome'
        : /Safari\//.test(ua)
          ? 'Safari'
          : null
  const so = /Android/.test(ua)
    ? 'Android'
    : /iPhone|iPad/.test(ua)
      ? 'iOS'
      : /Windows/.test(ua)
        ? 'Windows'
        : /Mac OS X/.test(ua)
          ? 'macOS'
          : /Linux/.test(ua)
            ? 'Linux'
            : null
  if (nav && so) return `${nav} em ${so}`
  return nav ?? so ?? (ua.trim() === '' ? 'Navegador desconhecido' : ua.slice(0, 60))
}
