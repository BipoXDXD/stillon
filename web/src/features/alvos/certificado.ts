import type { Alvo } from '@/lib/api/schemas'
import { dataHora, plural } from '@/lib/format'

const DIA_MS = 86_400_000

// Os dias que faltam, arredondados para cima: a 23 h do vencimento ainda
// falta "1 dia", e 0 é o próprio instante do vencimento. O aviso do PR 7
// conta igual (spec §5.2, item 6).
export function diasRestantes(notAfter: string, agora: Date): number {
  return Math.ceil((Date.parse(notAfter) - agora.getTime()) / DIA_MS)
}

// O sondador só lê o certificado em HTTPS com nome de host: com IP literal
// não há nome para conferir. O URL normaliza o IPv4 (0x7f.1 vira 127.0.0.1)
// e põe o IPv6 entre colchetes, então as duas formas bastam.
const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/

export function certificadoSeAplica(url: string): boolean {
  if (!URL.canParse(url)) return false
  const { protocol, hostname } = new URL(url)
  return protocol === 'https:' && !hostname.startsWith('[') && !IPV4.test(hostname)
}

export type FormaDoCertificado = 'valido' | 'vencido' | 'aguardando' | 'nao-se-aplica'
export type Certificado = { forma: FormaDoCertificado; texto: string }

// Com leitura, a validade vale mesmo que a URL tenha mudado: a API apaga a
// leitura quando a origem muda, então a que chega é do host cadastrado.
export function certificadoDoAlvo(alvo: Pick<Alvo, 'url' | 'tls'>, agora: Date): Certificado {
  if (alvo.tls === null) {
    return certificadoSeAplica(alvo.url)
      ? { forma: 'aguardando', texto: 'Certificado: aguardando a primeira sonda.' }
      : { forma: 'nao-se-aplica', texto: 'Certificado: não se aplica a este endereço.' }
  }
  const { not_after } = alvo.tls
  const n = diasRestantes(not_after, agora)
  if (n <= 0) return { forma: 'vencido', texto: `Certificado venceu em ${dataHora(not_after)}` }
  return { forma: 'valido', texto: `Certificado válido até ${dataHora(not_after)}, em ${plural(n, 'dia', 'dias')}` }
}

export function textoDoCertificado(alvo: Pick<Alvo, 'url' | 'tls'>, agora: Date): string {
  return certificadoDoAlvo(alvo, agora).texto
}
