import { z } from 'zod'
import type { AlteracaoAlvo, EntradaAlvo } from '@/lib/api/cliente'
import type { Alvo } from '@/lib/api/schemas'

// O formulário de alvo espelha internal/targets/validate.go. O servidor é a
// fonte da verdade: o espelho só antecipa o erro, e o 400 com fields cai no
// campo certo do mesmo jeito (um nome que resolve para IP interno, por
// exemplo, só o servidor recusa — e só no momento da conexão).

const PROIBIDOS = new Set([
  'host',
  'content-length',
  'transfer-encoding',
  'connection',
  'keep-alive',
  'upgrade',
  'te',
  'trailer',
  'expect',
  'proxy-authorization',
  'proxy-connection',
])

const NOME_DE_HEADER = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

const bytes = (s: string) => new TextEncoder().encode(s).length

// Um SUBCONJUNTO da lista do safedial, só para responder cedo aos casos
// óbvios. Nunca pode ser mais restritivo que o servidor: recusar aqui o que o
// servidor aceita seria um bug sem saída para o usuário.
function urlPermitida(raw: string): string | null {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return 'Use uma URL http:// ou https:// completa.'
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'Só http:// e https://.'
  const host = u.hostname.replace(/\.$/, '').toLowerCase()
  if (host === '') return 'Use uma URL http:// ou https:// completa.'
  if (host === 'localhost' || host.endsWith('.localhost'))
    return 'Endereços internos (localhost, redes privadas) não podem ser monitorados.'
  const v4 = host.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/)
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])]
    const interno =
      a === 0 ||
      a === 10 ||
      a === 127 ||
      a >= 224 ||
      (a === 169 && b === 254) ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && b === 168) ||
      (a === 100 && b >= 64 && b <= 127)
    if (interno) return 'Endereços internos (localhost, redes privadas) não podem ser monitorados.'
  }
  if (host.startsWith('[')) {
    const h = host.slice(1, -1)
    if (h === '::1' || h === '::' || /^f[cd]/.test(h) || /^fe[89ab]/.test(h)) {
      return 'Endereços internos (localhost, redes privadas) não podem ser monitorados.'
    }
  }
  return null
}

export const esquemaAlvo = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, 'Obrigatório.')
      .refine((s) => [...s].length <= 100, 'Até 100 caracteres.')
      .refine((s) => !/\p{Cc}/u.test(s), 'Sem caracteres de controle.'),
    url: z
      .string()
      .trim()
      .min(1, 'Obrigatório.')
      .refine((s) => bytes(s) <= 2048, 'Até 2048 caracteres.')
      .superRefine((s, ctx) => {
        const m = urlPermitida(s)
        if (m) ctx.addIssue({ code: 'custom', message: m })
      }),
    method: z.enum(['GET', 'HEAD', 'POST']),
    // Um dos intervalos da lista ou "outro" (spec §4.3): com "outro", o
    // número vem do campo em segundos; a regra é a mesma, 30 a 86400.
    intervaloEscolhido: z.string(),
    interval_sec: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(30, 'Mínimo de 30 s.')
      .max(86400, 'Máximo de 1 dia.'),
    timeout_ms: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(1000, 'Mínimo de 1000 ms.')
      .max(30000, 'Máximo de 30000 ms.'),
    expected_status: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(100, 'Entre 100 e 599.')
      .max(599, 'Entre 100 e 599.'),
    lentidao: z.enum(['padrao', 'personalizado', 'desativado']),
    degraded_threshold_ms: z
      .number({ error: 'Informe um número.' })
      .int()
      .min(1, 'Entre 1 e 60000 ms.')
      .max(60000, 'Entre 1 e 60000 ms.'),
    enabled: z.boolean(),
    // manter: a edição não mexe nos headers (os valores nunca voltam do
    // servidor); substituir: a lista abaixo troca todos; remover: apaga.
    modoHeaders: z.enum(['manter', 'substituir', 'remover']),
    headers: z.array(z.object({ nome: z.string().trim(), valor: z.string() })),
  })
  .superRefine((v, ctx) => {
    // D3 do Plano 2: o jitter de −10% antecipa a próxima sonda para 90% do
    // intervalo; um timeout maior deixaria duas sondas do alvo em voo.
    if (v.timeout_ms * 10 >= v.interval_sec * 9000) {
      ctx.addIssue({ code: 'custom', path: ['timeout_ms'], message: 'Tem de ser menor que 90% do intervalo.' })
    }
    if (v.modoHeaders !== 'substituir') return
    const vistos = new Set<string>()
    let total = 0
    if (v.headers.length > 20) ctx.addIssue({ code: 'custom', path: ['headers'], message: 'Até 20 headers.' })
    v.headers.forEach((h, i) => {
      const nome = h.nome.toLowerCase()
      if (!NOME_DE_HEADER.test(h.nome)) {
        ctx.addIssue({ code: 'custom', path: ['headers', i, 'nome'], message: 'Nome de header inválido.' })
      } else if (PROIBIDOS.has(nome)) {
        ctx.addIssue({
          code: 'custom',
          path: ['headers', i, 'nome'],
          message: 'Este header é controlado pelo monitor.',
        })
      } else if (vistos.has(nome)) {
        ctx.addIssue({ code: 'custom', path: ['headers', i, 'nome'], message: 'Header repetido.' })
      }
      vistos.add(nome)
      if (bytes(h.valor) > 1024) {
        ctx.addIssue({ code: 'custom', path: ['headers', i, 'valor'], message: 'Até 1 KiB.' })
      }
      total += bytes(h.nome) + bytes(h.valor)
    })
    if (total > 8192) ctx.addIssue({ code: 'custom', path: ['headers'], message: 'Os headers somam mais de 8 KiB.' })
  })

export type ValoresDoAlvo = z.infer<typeof esquemaAlvo>

export const INTERVALOS = [
  [30, '30 segundos'],
  [60, '1 minuto'],
  [300, '5 minutos'],
  [900, '15 minutos'],
  [3600, '1 hora'],
] as const

export const OUTRO = 'outro'

// O que o seletor mostra para um intervalo: ele mesmo, se está na lista;
// senão, "outro" (o campo em segundos aparece com o valor).
export function escolhaDoIntervalo(s: number): string {
  return INTERVALOS.some(([v]) => v === s) ? String(s) : OUTRO
}

export const valoresPadrao: ValoresDoAlvo = {
  name: '',
  url: '',
  method: 'GET',
  intervaloEscolhido: '60',
  interval_sec: 60,
  timeout_ms: 10000,
  expected_status: 200,
  lentidao: 'padrao',
  degraded_threshold_ms: 2000,
  enabled: true,
  modoHeaders: 'substituir',
  headers: [],
}

export function valoresDe(a: Alvo): ValoresDoAlvo {
  return {
    name: a.name,
    url: a.url,
    method: a.method,
    intervaloEscolhido: escolhaDoIntervalo(a.interval_sec),
    interval_sec: a.interval_sec,
    timeout_ms: a.timeout_ms,
    expected_status: a.expected_status,
    lentidao:
      a.degraded_threshold_ms === null ? 'desativado' : a.degraded_threshold_ms === 2000 ? 'padrao' : 'personalizado',
    degraded_threshold_ms: a.degraded_threshold_ms ?? 2000,
    enabled: a.enabled,
    modoHeaders: 'manter',
    headers: [],
  }
}

function limiar(v: ValoresDoAlvo): number | null | undefined {
  if (v.lentidao === 'desativado') return null
  if (v.lentidao === 'personalizado') return v.degraded_threshold_ms
  return 2000
}

function mapaDeHeaders(v: ValoresDoAlvo): Record<string, string> {
  return Object.fromEntries(v.headers.map((h) => [h.nome, h.valor]))
}

// O corpo do POST. Os números vão sempre explícitos (evita o "0 vira padrão
// em POST e 400 em PATCH" das dívidas do Plano 2); o limiar padrão vai
// ausente, que é o que o servidor lê como "use 2000 ms".
export function paraCriacao(v: ValoresDoAlvo): EntradaAlvo {
  const e: EntradaAlvo = {
    name: v.name,
    url: v.url,
    method: v.method,
    interval_sec: v.interval_sec,
    timeout_ms: v.timeout_ms,
    expected_status: v.expected_status,
    enabled: v.enabled,
  }
  if (v.lentidao !== 'padrao') e.degraded_threshold_ms = limiar(v)
  if (v.headers.length) e.headers = mapaDeHeaders(v)
  return e
}

// O corpo do PATCH: só o que mudou. O servidor recusa campo desconhecido, e
// "headers" só vai com "substituir" ou "remover" — mandar {} apagaria os
// headers em silêncio (spec do Plano 6, D24).
export function paraAlteracao(original: ValoresDoAlvo, v: ValoresDoAlvo): AlteracaoAlvo {
  const a: AlteracaoAlvo = {}
  if (v.name !== original.name) a.name = v.name
  if (v.url !== original.url) a.url = v.url
  if (v.method !== original.method) a.method = v.method
  if (v.interval_sec !== original.interval_sec) a.interval_sec = v.interval_sec
  if (v.timeout_ms !== original.timeout_ms) a.timeout_ms = v.timeout_ms
  if (v.expected_status !== original.expected_status) a.expected_status = v.expected_status
  if (v.enabled !== original.enabled) a.enabled = v.enabled
  if (limiar(v) !== limiar(original)) a.degraded_threshold_ms = limiar(v)
  if (v.modoHeaders === 'substituir') a.headers = mapaDeHeaders(v)
  if (v.modoHeaders === 'remover') a.headers = null
  return a
}

// Onde cada campo do servidor aparece no formulário.
export const campoDoServidor: Record<string, keyof ValoresDoAlvo> = {
  name: 'name',
  url: 'url',
  method: 'method',
  interval_sec: 'interval_sec',
  timeout_ms: 'timeout_ms',
  expected_status: 'expected_status',
  degraded_threshold_ms: 'degraded_threshold_ms',
  headers: 'headers',
}
