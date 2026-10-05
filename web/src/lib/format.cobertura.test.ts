import { describe, expect, it } from 'vitest'
import { haQuanto, milissegundos, navegadorDe } from './format'

// Expectativas da apresentação pública, com o relógio recebido por parâmetro.
// Sabotagem: apresentar a unidade de minutos para instantes separados por segundos.
describe('haQuanto: segundos e fronteira de um minuto', () => {
  const agora = new Date('2026-10-05T12:00:00Z')

  it.each([
    ['2026-10-05T11:59:45Z', 'há 15 segundos'],
    ['2026-10-05T12:00:15Z', 'em 15 segundos'],
    ['2026-10-05T12:00:00Z', 'agora'],
    ['2026-10-05T11:59:01Z', 'há 59 segundos'],
    ['2026-10-05T11:59:00Z', 'há 1 minuto'],
    ['2026-10-05T12:01:00Z', 'em 1 minuto'],
  ])('apresenta %s como “%s”', (instante, esperado) => {
    expect(haQuanto(instante, agora)).toBe(esperado)
  })
})

// O consumidor também recebe instantes fora da janela de horas.
// Sabotagem: escolher horas para intervalos que já completaram um dia.
describe('haQuanto: fronteira entre horas e dias', () => {
  const agora = new Date('2026-10-05T12:00:00Z')

  it.each([
    ['2026-10-04T13:00:00Z', 'há 23 horas'],
    ['2026-10-04T12:00:00Z', 'ontem'],
    ['2026-10-06T12:00:00Z', 'amanhã'],
    ['2026-10-02T12:00:00Z', 'há 3 dias'],
    ['2026-10-08T12:00:00Z', 'em 3 dias'],
  ])('apresenta %s como “%s”', (instante, esperado) => {
    expect(haQuanto(instante, agora)).toBe(esperado)
  })
})

// UAs são texto de terceiros. Estes exemplos cobrem os nomes suportados e
// sua precedência; não prometem identificar todo navegador existente.
// Sabotagens: priorizar Chrome sobre Edge, ou trocar um nome de navegador/SO.
describe('navegadorDe: precedência e sistemas reconhecidos', () => {
  it.each([
    [
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0',
      'Edge em Windows',
    ],
    [
      'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36',
      'Chrome em Android',
    ],
    [
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
      'Safari em iOS',
    ],
    [
      'Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
      'Safari em iOS',
    ],
    [
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15',
      'Safari em macOS',
    ],
  ])('identifica “%s” como “%s”', (ua, esperado) => {
    expect(navegadorDe(ua)).toBe(esperado)
  })
})

// O nome reconhecido continua útil quando o outro não está presente.
// Sabotagens: descartar nomes parciais ou retirar o limite do texto desconhecido.
describe('navegadorDe: identificação parcial e texto desconhecido', () => {
  it.each([
    ['Firefox/130.0', 'Firefox'],
    ['Mozilla/5.0 (X11; Linux x86_64)', 'Linux'],
    ['', 'Navegador desconhecido'],
    [' \t\n ', 'Navegador desconhecido'],
    ['curl/8.0', 'curl/8.0'],
    ['  agente particular  ', '  agente particular  '],
    [
      '012345678901234567890123456789012345678901234567890123456789EXCEDENTE',
      '012345678901234567890123456789012345678901234567890123456789',
    ],
  ])('apresenta o fallback de “%s” como “%s”', (ua, esperado) => {
    expect(navegadorDe(ua)).toBe(esperado)
  })
})

// Ausência do dado não vira zero; latência fracionária é exibida arredondada.
// Sabotagens: apresentar undefined como latência ou truncar em vez de arredondar.
describe('milissegundos: ausência e arredondamento', () => {
  it.each([
    [undefined, '—'],
    [0, '0 ms'],
    [1.49, '1 ms'],
    [1.5, '2 ms'],
  ])('apresenta %s como “%s”', (valor, esperado) => {
    expect(milissegundos(valor)).toBe(esperado)
  })
})
