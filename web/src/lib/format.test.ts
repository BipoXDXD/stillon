import { describe, expect, it } from 'vitest'
import { duracao, haQuanto, milissegundos, navegadorDe, pontosDeCodigo, porcento } from './format'

describe('porcento', () => {
  it('trunca em duas casas, nunca arredonda para cima', () => {
    expect(porcento(0.99999)).toBe('99,99%')
    expect(porcento(0.9993)).toBe('99,93%')
    expect(porcento(1)).toBe('100,00%')
    expect(porcento(0)).toBe('0,00%')
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
