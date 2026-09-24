import { z } from 'zod'

// A CSP do painel não tem 'unsafe-eval'. O Zod 4 decide se compila
// validadores rápidos testando Function(''): o teste é pego num try, mas o
// navegador reporta a violação de script-src mesmo assim (achado no E2E da
// cópia). jitless desliga o teste e o JIT; a validação é a mesma.
z.config({ jitless: true })

// Os schemas das respostas da API, campo a campo com os DTOs Go
// (internal/httpapi/*handlers.go) e o contrato dos Planos 5–7.
//
// Toda resposta passa por aqui (D10 da spec do Plano 6). O z.object padrão
// descarta chave extra, então um campo novo no servidor não quebra o painel;
// e falha com chave ausente ou tipo trocado, então uma remoção quebra alto,
// com o nome do campo, em vez de virar undefined numa tela.

const instante = z.string().min(1)
const id = z.string().min(1)

export const Token = z.object({ access_token: z.string().min(1), expires_in: z.number() })
export type Token = z.infer<typeof Token>

export const RespostaLogin = z.union([
  Token,
  z.object({ totp_required: z.literal(true), challenge: z.string().min(1) }),
])
export type RespostaLogin = z.infer<typeof RespostaLogin>

export const Me = z.object({ id, email: z.string(), totp_enabled: z.boolean(), created_at: instante })
export type Me = z.infer<typeof Me>

export const Alvo = z.object({
  id,
  name: z.string(),
  url: z.string(),
  method: z.enum(['GET', 'HEAD', 'POST']),
  interval_sec: z.number(),
  timeout_ms: z.number(),
  expected_status: z.number(),
  degraded_threshold_ms: z.number().nullable(),
  enabled: z.boolean(),
  header_names: z.array(z.string()),
  created_at: instante,
})
export type Alvo = z.infer<typeof Alvo>

export const ListaDeAlvos = z.object({ items: z.array(Alvo), next_cursor: z.string().optional() })

export const EstadoAlvo = z.enum(['up', 'degraded', 'down', 'unknown'])
export type EstadoAlvo = z.infer<typeof EstadoAlvo>

export const HoraDoSpark = z.object({
  bucket: instante,
  total: z.number(),
  ok: z.number(),
  p50_ms: z.number().nullable(),
})
export type HoraDoSpark = z.infer<typeof HoraDoSpark>

export const ItemDoResumo = z.object({
  id,
  name: z.string(),
  enabled: z.boolean(),
  state: EstadoAlvo,
  last_checked_at: instante.nullable(),
  last_latency_ms: z.number().nullable(),
  uptime_24h: z.number().nullable(),
  open_incident_id: id.nullable(),
  spark: z.array(HoraDoSpark),
})
export type ItemDoResumo = z.infer<typeof ItemDoResumo>

export const Resumo = z.object({ items: z.array(ItemDoResumo) })

export const Check = z.object({
  checked_at: instante,
  status: z.enum(['up', 'degraded', 'down']),
  latency_ms: z.number(),
  status_code: z.number().nullable(),
  error_kind: z.string().nullable(),
})
export type Check = z.infer<typeof Check>

export const Hora = z.object({
  bucket: instante,
  total: z.number(),
  ok: z.number(),
  p50_ms: z.number().nullable(),
  p95_ms: z.number().nullable(),
  p99_ms: z.number().nullable(),
  max_ms: z.number().nullable(),
})
export type Hora = z.infer<typeof Hora>

// resolution discrimina o formato dos itens: o painel desenha pelo que o
// servidor decidiu, nunca pela janela que pediu (spec §9.1).
export const Serie = z.discriminatedUnion('resolution', [
  z.object({ resolution: z.literal('raw'), items: z.array(Check) }),
  z.object({ resolution: z.literal('1h'), items: z.array(Hora) }),
])
export type Serie = z.infer<typeof Serie>

export const Incidente = z.object({
  id,
  target_id: id,
  target_name: z.string(),
  started_at: instante,
  resolved_at: instante.nullable(),
  cause: z.string(),
  acknowledged_at: instante.nullable(),
})
export type Incidente = z.infer<typeof Incidente>

export const ListaDeIncidentes = z.object({ items: z.array(Incidente), next_cursor: z.string().optional() })
export type ListaDeIncidentes = z.infer<typeof ListaDeIncidentes>

export const Canal = z.object({
  id,
  kind: z.enum(['discord', 'email']),
  hint: z.string(),
  status: z.enum(['verified', 'pending', 'expired', 'failing']),
  enabled: z.boolean(),
  created_at: instante,
  last_error: z.string().nullable(),
  last_failure_at: instante.nullable(),
})
export type Canal = z.infer<typeof Canal>

export const ListaDeCanais = z.object({ items: z.array(Canal) })

export const ItemDaPagina = z.object({ target_id: id, display_name: z.string() })
export type ItemDaPagina = z.infer<typeof ItemDaPagina>

export const PaginaDeStatus = z.object({
  id,
  slug: z.string(),
  title: z.string(),
  published: z.boolean(),
  items: z.array(ItemDaPagina),
  public_url: z.string(),
  created_at: instante,
})
export type PaginaDeStatus = z.infer<typeof PaginaDeStatus>

export const ListaDePaginas = z.object({ items: z.array(PaginaDeStatus) })

export const Sessao = z.object({
  id,
  created_at: instante,
  last_used_at: instante,
  user_agent: z.string(),
  current: z.boolean(),
})
export type Sessao = z.infer<typeof Sessao>

export const ListaDeSessoes = z.object({ items: z.array(Sessao) })

export const CadastroTotp = z.object({ otpauth_uri: z.string().startsWith('otpauth://') })
export const ConfirmacaoTotp = z.object({ recovery_codes: z.array(z.string()) })
