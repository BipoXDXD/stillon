import { z } from 'zod'

// A CSP do painel não tem 'unsafe-eval'. O Zod 4 decide se compila
// validadores rápidos testando Function(''): o teste é pego num try, mas o
// navegador reporta a violação de script-src mesmo assim (achado no E2E da
// cópia). jitless desliga o teste e o JIT; a validação é a mesma.
z.config({ jitless: true })

// Os schemas das respostas da API, campo a campo com os DTOs Go
// (internal/httpapi/*handlers.go) e o contrato da API.
//
// Toda resposta passa por aqui. O z.object padrão
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

export const RegraDeConteudo = z.object({
  mode: z.enum(['contains', 'not_contains', 'json_equals']),
  value: z.string(),
  // Sempre presente na resposta: null fora de json_equals.
  json_pointer: z.string().nullable(),
})
export type RegraDeConteudo = z.infer<typeof RegraDeConteudo>

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
  tags: z.array(z.string()),
  // O aviso de certificado (o interruptor entra no painel no PR 7) e a última
  // leitura da validade; null enquanto nenhuma sonda leu o certificado.
  tls_warn: z.boolean(),
  tls: z.object({ not_after: instante, observed_at: instante }).nullable(),
  content_rule: RegraDeConteudo.nullable(),
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
  tags: z.array(z.string()),
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

// Os heartbeats (spec de heartbeats, §7). O incidente de heartbeat tem forma
// própria: `GET /incidents` não os traz.
// Um valor que uma versão posterior do servidor criou (piso de rollback)
// vira 'unknown': uma aba aberta antes do deploy não pode falhar no parse da
// lista inteira por um estado ou tipo de sinal que ela não conhece. Só texto:
// outro tipo continua erro de contrato. Aceitar qualquer texto é de propósito:
// o custo é que um erro de digitação do servidor vira "Estado desconhecido" (ou
// "Outro sinal") na tela, em vez de falhar alto como erro de contrato.
const desconhecido = z.string().transform((): 'unknown' => 'unknown')

export const EstadoDoHeartbeat = z.union([z.enum(['waiting', 'up', 'grace', 'late', 'failing', 'paused']), desconhecido])
export type EstadoDoHeartbeat = z.infer<typeof EstadoDoHeartbeat>

// 'unknown' é a linha de um agendamento que esta versão não sabe ler: o
// servidor manda só o kind, e o PATCH nela é 409 schedule_unsupported. Um
// servidor posterior pode mandar o kind novo, e o painel trata igual.
const PorPeriodo = z.object({ kind: z.literal('period'), period_sec: z.number() })
const PorCron = z.object({ kind: z.literal('cron'), expr: z.string(), timezone: z.string() })

export const AgendamentoEditavel = z.discriminatedUnion('kind', [PorPeriodo, PorCron])
export type AgendamentoEditavel = z.infer<typeof AgendamentoEditavel>

// Um kind texto que esta versão não conhece (o 'oncalendar' de uma versão
// posterior) vira { kind: 'unknown' } antes da união, como o servidor faz com a
// linha que não sabe ler; period e cron conhecidos continuam exigindo os campos.
const KINDS_CONHECIDOS: readonly string[] = ['period', 'cron', 'unknown']

function normalizarKindDesconhecido(valor: unknown): unknown {
  if (typeof valor !== 'object' || valor === null || !('kind' in valor)) {
    return valor
  }
  const { kind } = valor
  return typeof kind === 'string' && !KINDS_CONHECIDOS.includes(kind) ? { kind: 'unknown' } : valor
}

export const Agendamento = z.preprocess(
  normalizarKindDesconhecido,
  z.discriminatedUnion('kind', [PorPeriodo, PorCron, z.object({ kind: z.literal('unknown') })]),
)
export type Agendamento = z.infer<typeof Agendamento>

// Sem ping_url, de propósito: o z.object descarta o campo, e o segredo não
// entra no cache por este tipo.
export const Heartbeat = z.object({
  id,
  name: z.string(),
  description: z.string(),
  tags: z.array(z.string()),
  enabled: z.boolean(),
  schedule: Agendamento,
  grace_sec: z.number(),
  ping_resumes: z.boolean(),
  state: EstadoDoHeartbeat,
  deadline_at: instante,
  last_ping_at: instante.nullable(),
  last_success_at: instante.nullable(),
  last_start_at: instante.nullable(),
  last_runtime_ms: z.number().nullable(),
  open_incident_id: id.nullable(),
  created_at: instante,
})
export type Heartbeat = z.infer<typeof Heartbeat>

// Só a resposta de criar e de rotacionar: ela é entregue a quem pediu e não
// passa pelo cache de consultas.
export const HeartbeatComPing = Heartbeat.extend({ ping_url: z.url() })

export const ListaDeHeartbeats = z.object({ items: z.array(Heartbeat), next_cursor: z.string().optional() })

export const PreviaDoCron = z.object({ next_runs: z.array(instante) })

// O rid que a tarefa mandou: o servidor aceita qualquer hex 8-4-4-4-12, sem
// exigir versão nem variante de UUID, e o guarda em minúsculas. A origem é enum
// estrito, ao contrário do kind: uma origem nova é mudança de contrato (spec §8).
export const EventoDeHeartbeat = z.object({
  id,
  at: instante,
  kind: z.union([z.enum(['start', 'success', 'fail', 'log']), desconhecido]),
  runtime_ms: z.number().nullable(),
  exit_code: z.number().nullable(),
  run_id: z.guid().nullable(),
  source: z.enum(['ping', 'panel']),
})
export type EventoDeHeartbeat = z.infer<typeof EventoDeHeartbeat>

export const ListaDeEventos = z.object({ items: z.array(EventoDeHeartbeat), next_cursor: z.string().optional() })

export const IncidenteDeHeartbeat = z.object({
  id,
  heartbeat_id: id,
  heartbeat_name: z.string(),
  started_at: instante,
  resolved_at: instante.nullable(),
  cause: z.string(),
  acknowledged_at: instante.nullable(),
})
export type IncidenteDeHeartbeat = z.infer<typeof IncidenteDeHeartbeat>

export const ListaDeIncidentesDeHeartbeat = z.object({ items: z.array(IncidenteDeHeartbeat), next_cursor: z.string().optional() })

// Os tipos que esta versão conhece: o teste de contrato exige um caso do Go
// para cada um.
export const TiposConhecidosDeCanal = z.enum(['discord', 'email'])

// Um tipo de canal que uma versão posterior do servidor criou (piso de
// rollback) vira 'desconhecido', pelo mesmo motivo do `desconhecido` acima: a
// lista inteira não pode cair por uma linha. Só texto; sem kind continua erro.
export const TipoDeCanal = TiposConhecidosDeCanal.or(z.string().transform(() => 'desconhecido' as const))
export type TipoDeCanal = z.infer<typeof TipoDeCanal>

export const Canal = z.object({
  id,
  kind: TipoDeCanal,
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
// O rotate devolve também o rotation: 16 bytes em hex que
// identificam o pendente. O confirm o devolve, e um pendente substituído por
// outro rotate da mesma sessão responde 403, como "sem troca".
export const InicioDaTroca = CadastroTotp.extend({ rotation: z.string().regex(/^[0-9a-f]{32}$/) })
export const ConfirmacaoTotp = z.object({ recovery_codes: z.array(z.string()) })
