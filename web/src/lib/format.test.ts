import { describe, expect, it } from 'vitest'
import { duracao, haQuanto, horaMinuto, milissegundos, navegadorDe, plural, pontosDeCodigo, porcento } from './format'

describe('porcento', () => {
  it('trunca em duas casas, nunca arredonda para cima', () => {
    expect(porcento(0.99999)).toBe('99,99%')
    expect(porcento(0.9993)).toBe('99,93%')
    expect(porcento(1)).toBe('100,00%')
    expect(porcento(0)).toBe('0,00%')
  })

  // 0.0029 * 10000 é 28,999…96 em ponto flutuante: um floor seco mostraria
  // "0,28%". Sabotagem prevista: tirar o "+ 1e-9" de porcento().
  it('valores exatos não perdem a última casa', () => {
    expect(porcento(0.0029)).toBe('0,29%')
    expect(porcento(0.5799)).toBe('57,99%')
    expect(porcento(0.9999)).toBe('99,99%')
  })
})

describe('milissegundos', () => {
  it('null é traço', () => {
    expect(milissegundos(null)).toBe('—')
    expect(milissegundos(1234)).toBe('1.234 ms')
  })
})

describe('duracao', () => {
  it('escolhe a unidade', () => {
    expect(duracao(20_000)).toBe('menos de 1 min')
    expect(duracao(12 * 60_000)).toBe('12 min')
    expect(duracao(64 * 60_000)).toBe('1 h 4 min')
    expect(duracao(51 * 3600_000)).toBe('2 d 3 h')
  })
})

describe('haQuanto', () => {
  it('fala em minutos e horas', () => {
    const agora = new Date('2026-09-24T12:00:00Z')
    expect(haQuanto('2026-09-24T11:58:00Z', agora)).toBe('há 2 minutos')
    expect(haQuanto('2026-09-24T09:00:00Z', agora)).toBe('há 3 horas')
  })
})

// A unidade é escolhida depois de arredondar: 59 min 50 s não pode virar
// "há 60 minutos", nem 23 h 50 min "há 24 horas". O arredondamento é
// simétrico entre passado e futuro (Math.round(-59.5) daria -59).
// Sabotagens: escolher a unidade pelo valor bruto, antes de arredondar; ou
// arredondar o valor com sinal.
describe('haQuanto: arredonda antes de escolher a unidade', () => {
  const agora = new Date('2026-10-05T12:00:00Z')

  it.each([
    ['2026-10-05T11:59:00.6Z', 'há 59 segundos'],
    ['2026-10-05T11:59:00.4Z', 'há 1 minuto'],
    ['2026-10-05T11:01:31Z', 'há 58 minutos'],
    ['2026-10-05T11:00:31Z', 'há 59 minutos'],
    ['2026-10-05T11:00:30Z', 'há 1 hora'],
    ['2026-10-05T11:00:10Z', 'há 1 hora'],
    ['2026-10-05T12:59:50Z', 'em 1 hora'],
    ['2026-10-05T11:00:00Z', 'há 1 hora'],
    ['2026-10-04T12:30:01Z', 'há 23 horas'],
    ['2026-10-04T12:30:00Z', 'ontem'],
    ['2026-10-04T12:10:00Z', 'ontem'],
    ['2026-10-06T11:50:00Z', 'amanhã'],
  ])('apresenta %s como “%s”', (instante, esperado) => {
    expect(haQuanto(instante, agora)).toBe(esperado)
  })
})

describe('pontosDeCodigo', () => {
  it('conta code points, não unidades UTF-16', () => {
    expect(pontosDeCodigo('😀'.repeat(15))).toBe(15)
    expect('😀'.repeat(15).length).toBe(30)
  })
})

describe('navegadorDe', () => {
  it('reconhece os comuns e não inventa', () => {
    expect(navegadorDe('Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0')).toBe('Firefox em Linux')
    expect(navegadorDe('Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/128.0 Safari/537.36')).toBe(
      'Chrome em Windows',
    )
    expect(navegadorDe('')).toBe('Navegador desconhecido')
    expect(navegadorDe('curl/8.0')).toBe('curl/8.0')
  })
})

describe('plural', () => {
  it.each([
    [0, '0 canais'],
    [1, '1 canal'],
    [2, '2 canais'],
    [100, '100 canais'],
  ])('%i → "%s"', (n, texto) => {
    expect(plural(n, 'canal', 'canais')).toBe(texto)
  })
})

// Datas pelo construtor local: o teste vale em qualquer fuso.
describe('horaMinuto', () => {
  it('hora e minuto com dois dígitos, no fuso do navegador', () => {
    expect(horaMinuto(new Date(2026, 9, 14, 9, 5).getTime())).toBe('09:05')
    expect(horaMinuto(new Date(2026, 9, 14, 23, 59).toISOString())).toBe('23:59')
  })
})
