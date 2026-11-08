import { createHmac } from 'node:crypto'

// Um aplicativo autenticador para o E2E (RFC 6238, os parâmetros do servidor:
// SHA-1, 6 dígitos, 30 s; internal/auth/totp.go). O servidor gasta o
// timestep de cada código aceito (Plano 8; A4 do Plano 10), e um código do
// mesmo timestep, mesmo de outro segredo, pode ser recusado: codigoNovo
// espera o próximo timestep quando o atual já foi usado.

const PERIODO_S = 30

function base32(texto: string): Buffer {
  const alfabeto = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const c of texto.replace(/=+$/, '').toUpperCase()) {
    const v = alfabeto.indexOf(c)
    if (v < 0) throw new Error(`E2E: caractere fora do base32 na chave: ${c}`)
    bits += v.toString(2).padStart(5, '0')
  }
  const bytes: number[] = []
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2))
  return Buffer.from(bytes)
}

export function codigoDoPasso(chave: string, passo: number): string {
  const contador = Buffer.alloc(8)
  contador.writeBigUInt64BE(BigInt(passo))
  const h = createHmac('sha1', base32(chave)).update(contador).digest()
  const o = h[h.length - 1]! & 0x0f
  const n = (h.readUInt32BE(o) & 0x7fffffff) % 1_000_000
  return String(n).padStart(6, '0')
}

const passoAgora = () => Math.floor(Date.now() / 1000 / PERIODO_S)

// Os timesteps já entregues por este autenticador (para todas as chaves).
let ultimoPasso = -1

// Um código de um timestep que nenhum código anterior usou, sem esperar o
// relógio quando a folga do servidor permite. O servidor aceita o passo
// anterior, o atual e o SEGUINTE (skew 1, internal/auth/totp.go: o celular um
// pouco adiantado) e gasta o passo de cada código aceito. Um autenticador que
// só desse o passo atual faria cada código depois do primeiro esperar a virada
// de 30 s; aqui, quando o atual já foi usado (ou está a menos de 3 s do fim e
// poderia vencer entre a digitação e o servidor), sai o código do passo
// seguinte, que o servidor ainda aceita por 30 s ou mais. Só espera quando o
// seguinte também já foi usado. Cada código continua de um passo novo: o
// servidor recusaria um repetido, e é essa recusa que os testes Go cobrem.
export async function codigoNovo(chave: string): Promise<string> {
  for (;;) {
    const agoraMs = Date.now()
    const passo = passoAgora()
    const restaMs = (passo + 1) * PERIODO_S * 1000 - agoraMs
    const candidato = passo > ultimoPasso && restaMs > 3_000 ? passo : passo + 1
    if (candidato > ultimoPasso) {
      ultimoPasso = candidato
      return codigoDoPasso(chave, candidato)
    }
    await new Promise((ok) => setTimeout(ok, restaMs + 100))
  }
}
