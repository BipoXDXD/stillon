import { describe, expect, it } from 'vitest'
import { dataHora } from '@/lib/format'
import { certificadoSeAplica, diasRestantes, textoDoCertificado } from './certificado'

// A mesma tabela vale para a função Go do PR 7: painel e aviso contam os dias
// do mesmo jeito (spec §5.2, item 6).
const NOT_AFTER = '2026-12-01T00:00:00Z'

describe('diasRestantes', () => {
  it.each([
    { agora: '2026-11-17T00:00:00Z', dias: 14 },
    { agora: '2026-11-16T23:59:59Z', dias: 15 },
    { agora: '2026-11-30T19:00:00Z', dias: 1 },
    { agora: '2026-12-01T00:00:00Z', dias: 0 },
    { agora: '2026-12-02T00:00:00Z', dias: -1 },
  ])('em $agora faltam $dias', ({ agora, dias }) => {
    expect(diasRestantes(NOT_AFTER, new Date(agora))).toBe(dias)
  })
})

describe('certificadoSeAplica', () => {
  it.each(['https://exemplo.com', 'https://exemplo.com.', 'https://exemplo.com:8443'])('%s: sim', (url) => {
    expect(certificadoSeAplica(url)).toBe(true)
  })

  it.each(['http://exemplo.com', 'https://203.0.113.5', 'https://[2001:db8::1]', 'nao é url'])('%s: não', (url) => {
    expect(certificadoSeAplica(url)).toBe(false)
  })

  // O URL normaliza estas formas para IPv4: 1.2.3.4, 127.0.0.1 e 127.0.0.1.
  it.each(['https://1.2.3.4.', 'https://2130706433', 'https://0x7f.1'])('%s, IPv4 disfarçado: não', (url) => {
    expect(certificadoSeAplica(url)).toBe(false)
  })
})

describe('textoDoCertificado', () => {
  const leitura = { not_after: NOT_AFTER, observed_at: '2026-11-10T00:00:00Z' }
  const https = 'https://exemplo.com/saude'

  it('válido, no plural', () => {
    expect(textoDoCertificado({ url: https, tls: leitura }, new Date('2026-11-17T00:00:00Z'))).toBe(
      `Certificado válido até ${dataHora(NOT_AFTER)}, em 14 dias`,
    )
  })

  it('válido, no singular', () => {
    expect(textoDoCertificado({ url: https, tls: leitura }, new Date('2026-11-30T19:00:00Z'))).toBe(
      `Certificado válido até ${dataHora(NOT_AFTER)}, em 1 dia`,
    )
  })

  it.each(['2026-12-01T00:00:00Z', '2026-12-02T00:00:00Z'])('em %s, venceu', (agora) => {
    expect(textoDoCertificado({ url: https, tls: leitura }, new Date(agora))).toBe(`Certificado venceu em ${dataHora(NOT_AFTER)}`)
  })

  it('sem leitura e sem HTTPS com nome: não se aplica', () => {
    expect(textoDoCertificado({ url: 'http://exemplo.com', tls: null }, new Date(NOT_AFTER))).toBe(
      'Certificado: não se aplica a este endereço.',
    )
  })

  it('sem leitura, com HTTPS: aguarda a primeira sonda', () => {
    expect(textoDoCertificado({ url: https, tls: null }, new Date(NOT_AFTER))).toBe('Certificado: aguardando a primeira sonda.')
  })
})
