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

// Os timestamps de time.Time incluem fuso e podem ter fração de segundo.
const instante = z.iso.datetime({ offset: true })
const id = z.string().min(1)

const urlPublica = z.string().refine((valor) => {
  // O Compose local usa HTTP. Exija a autoridade explícita e sem credenciais,
  // pois o parser do navegador pode consertar URLs sem as barras ou sem host.
  if (!/^https?:\/\/[^/?#\\@\s]+(?:[/?#]|$)/i.test(valor)) return false
  // net/url recusa controles ASCII, inclusive os que URL remove ao ler o caminho.
  for (const caractere of valor) {
    const codigo = caractere.charCodeAt(0)
    if (codigo < 0x20 || codigo === 0x7f) return false
  }
  try {
    return new URL(valor).hostname !== ''
  } catch {
    return false
  }
}, 'URL pública HTTP ou HTTPS inválida.')

export const Token = z.object({ access_token: z.string().min(1), expires_in: z.number() })
export type Token = z.infer<typeof Token>

export const RespostaLogin = z.union([
  Token,
  z.object({ totp_required: z.literal(true), challenge: z.string().min(1) }),
])
export type RespostaLogin = z.infer<typeof RespostaLogin>

export const Me = z.object({
  id,
  email: z.string(),
  totp_enabled: z.boolean(),
  // false enquanto o e-mail do cadastro público não for confirmado: o
  // servidor recusa criar recursos com 403 email_not_verified.
  email_verified: z.boolean(),
  created_at: instante,
})
export type Me = z.infer<typeof Me>

// GET /auth/signup-config: a site key vem vazia com o cadastro desligado.
// terms_version fica para detectar divergência entre o painel (versao.json) e
// o servidor; o cadastro envia a versão do painel.
export const ConfigDoCadastro = z.object({ enabled: z.boolean(), turnstile_site_key: z.string(), terms_version: z.string() })
export type ConfigDoCadastro = z.infer<typeof ConfigDoCadastro>

// O papel de quem pede no projeto (projectResponse.role). Um papel de uma
// versão posterior do servidor vira 'desconhecido', no molde do
// EstadoDoHeartbeat: a lista de projetos não pode falhar inteira por ele.
export const Papel = z.union([
  z.enum(['owner', 'manager', 'member', 'viewer']),
  z.string().transform((): 'desconhecido' => 'desconhecido'),
])
export type Papel = z.infer<typeof Papel>

// As cotas do TITULAR do projeto (projectResponse.limits), não as de quem
// pede. Opcional: a API anterior, ainda no ar durante o release, não as manda.
export const LimitesDoProjeto = z.object({
  projects: z.number().int(),
  members_per_project: z.number().int(),
  targets: z.number().int(),
  heartbeats: z.number().int(),
  channels_per_project: z.number().int(),
  min_interval_sec: z.number().int(),
})
export type LimitesDoProjeto = z.infer<typeof LimitesDoProjeto>

export const Projeto = z.object({
  id,
  name: z.string(),
  is_default: z.boolean(),
  role: Papel,
  created_at: instante,
  limits: LimitesDoProjeto.optional(),
  // A titularidade foi proposta a quem pede (PR 4.3). Opcional: o binário do
  // 4.3, ainda no ar durante o release, só o manda quando é true.
  pending_titular: z.boolean().optional(),
})
export type Projeto = z.infer<typeof Projeto>

export const ListaDeProjetos = z.object({ items: z.array(Projeto) })
export type ListaDeProjetos = z.infer<typeof ListaDeProjetos>

// Um membro do projeto (memberResponse). pending_titular: a titularidade foi
// proposta a ele; opcional pelo mesmo release do Projeto.
export const Membro = z.object({
  user_id: id,
  email: z.string(),
  role: Papel,
  is_titular: z.boolean(),
  pending_titular: z.boolean().optional(),
  joined_at: instante,
})
export type Membro = z.infer<typeof Membro>

export const ListaDeMembros = z.object({ items: z.array(Membro) })
export type ListaDeMembros = z.infer<typeof ListaDeMembros>

// Um convite pendente (invitationResponse). O token só vai no e-mail.
export const Convite = z.object({
  id,
  email: z.string(),
  role: Papel,
  created_at: instante,
  expires_at: instante,
})
export type Convite = z.infer<typeof Convite>

export const ListaDeConvites = z.object({ items: z.array(Convite) })
export type ListaDeConvites = z.infer<typeof ListaDeConvites>

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
  public_url: urlPublica,
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
