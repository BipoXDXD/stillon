import type { z } from 'zod'

// O transporte cru da API: prefixo, cabeçalhos e leitura do envelope de erro
// (spec §9.1). Não sabe nada de sessão; quem renova o token é cliente.ts.

export const PREFIXO = '/api/v1'

export type Buscar = (entrada: string, init: RequestInit) => Promise<Response>

export type ErroDeCampo = { field: string; reason: string }

// ApiError é a única forma de erro que chega às telas. code é o contrato com
// o servidor; 'network' e 'unexpected' são do cliente: rede caída e resposta
// que não é o envelope (502 do proxy, HTML, JSON inválido).
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: ErroDeCampo[]
  readonly traceId: string | null

  constructor(status: number, code: string, message: string, fields: ErroDeCampo[] = [], traceId: string | null = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.fields = fields
    this.traceId = traceId
  }
}

// O objeto não existe (mais): apagado em outra aba, ou nunca existiu. As
// telas tratam igual: explicam, tiram do cache e voltam à lista.
export function naoEncontrado(e: unknown): boolean {
  return e instanceof ApiError && e.status === 404
}

type Envelope = {
  error?: { code?: unknown; message?: unknown; fields?: unknown }
  trace_id?: unknown
}

function camposDe(v: unknown): ErroDeCampo[] {
  if (!Array.isArray(v)) return []
  return v.filter(
    (f): f is ErroDeCampo =>
      typeof f === 'object' && f !== null && typeof f.field === 'string' && typeof f.reason === 'string',
  )
}

export async function lerErro(r: Response): Promise<ApiError> {
  let corpo: Envelope | null = null
  try {
    corpo = (await r.json()) as Envelope
  } catch {
    corpo = null
  }
  const e = corpo?.error
  if (!e || typeof e.code !== 'string') {
    return new ApiError(r.status, 'unexpected', `Resposta inesperada do servidor (${r.status}).`)
  }
  const traceId = typeof corpo?.trace_id === 'string' && corpo.trace_id !== '' ? corpo.trace_id : null
  return new ApiError(r.status, e.code, typeof e.message === 'string' ? e.message : '', camposDe(e.fields), traceId)
}

// lerCorpo lê uma resposta de sucesso pelo schema da rota. JSON quebrado ou
// fora do contrato vira ApiError 'unexpected' com o status da resposta, e
// nunca uma exceção crua na tela (spec do Plano 6 §3.4): a tela mostra a
// mensagem de sempre, e o QueryClient não repete (status < 500). A mensagem
// leva os caminhos dos campos que falharam, para o log e para o suporte.
export async function lerCorpo<T>(r: Response, esquema: z.ZodType<T>): Promise<T> {
  let json: unknown
  try {
    json = await r.json()
  } catch {
    throw new ApiError(r.status, 'unexpected', `Resposta sem JSON válido (${r.status}).`)
  }
  const v = esquema.safeParse(json)
  if (!v.success) {
    const campos = v.error.issues.map((i) => i.path.join('.') || '(raiz)').join(', ')
    throw new ApiError(r.status, 'unexpected', `Resposta fora do contrato: ${campos}.`)
  }
  return v.data
}

export type Pedido = {
  metodo: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  caminho: string
  corpo?: unknown
  token?: string | null
  sinal?: AbortSignal
}

// enviar monta a requisição. Toda mutação leva X-Requested-With, inclusive
// /auth/refresh, /auth/login e /auth/logout: o RequireCustomHeader envolve a
// API inteira (routes.go), e sem o cabeçalho a resposta é 403 (spec §5.3).
export async function enviar(buscar: Buscar, p: Pedido): Promise<Response> {
  const cabecalhos: Record<string, string> = { Accept: 'application/json' }
  if (p.metodo !== 'GET') cabecalhos['X-Requested-With'] = 'vigilume'
  if (p.token) cabecalhos.Authorization = `Bearer ${p.token}`
  const init: RequestInit = { method: p.metodo, headers: cabecalhos, credentials: 'same-origin', signal: p.sinal }
  if (p.corpo !== undefined) {
    cabecalhos['Content-Type'] = 'application/json'
    init.body = JSON.stringify(p.corpo)
  }
  try {
    return await buscar(PREFIXO + p.caminho, init)
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network', 'Sem conexão com o servidor.')
  }
}
