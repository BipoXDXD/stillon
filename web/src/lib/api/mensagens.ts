import { plural } from '@/lib/format'
import { ApiError } from './http'

// Tradução de vocabulário fechado para texto. Nada do que o servidor manda
// além do code (e do trace_id) aparece na tela: a message do envelope é
// genérica de propósito (spec §9.1), e a tela sabe dizer melhor.

const porCodigo: Record<string, string> = {
  network: 'Sem conexão com o servidor. Confira a rede e tente de novo.',
  unexpected: 'O servidor respondeu algo inesperado.',
  internal_error: 'Erro interno do servidor.',
  unavailable: 'Serviço indisponível no momento.',
  rate_limited: 'Muitas requisições. Aguarde um minuto e tente de novo.',
  payload_too_large: 'Os dados enviados são grandes demais.',
  not_found: 'Não encontrado.',
  // B5 do Plano 10: o 405 da API vem no envelope, com código próprio.
  method_not_allowed: 'O servidor não aceita esta operação. O painel pode estar desatualizado: recarregue a página.',
  forbidden: 'Operação não permitida.',
  unauthorized: 'Sua sessão terminou. Entre de novo.',
  invalid_session: 'Sua sessão terminou. Entre de novo.',
  invalid_credentials: 'E-mail ou senha incorretos.',
  validation_failed: 'Confira os campos destacados.',
  target_limit_reached: 'Limite de 100 alvos atingido.',
  heartbeat_limit_reached: 'Limite de 100 heartbeats atingido.',
  channel_limit_reached: 'Limite de 10 canais atingido.',
  channel_not_pending: 'O canal não está mais aguardando verificação.',
  delivery_failed: 'Não conseguimos entregar a mensagem de teste. Confira o endereço.',
  status_page_exists: 'Você já tem uma status page.',
  slug_taken: 'Este endereço já está em uso.',
  // O 409 do PATCH num heartbeat de agendamento que esta versão não lê.
  schedule_unsupported: 'Este agendamento não pode ser editado nesta versão.',
}

function duracao(segundos: number): string {
  if (segundos <= 1) return '1 segundo'
  if (segundos < 60) return `${segundos} segundos`
  const minutos = Math.ceil(segundos / 60)
  return plural(minutos, 'minuto', 'minutos')
}

export function mensagemDe(e: unknown): string {
  if (e instanceof ApiError) {
    // O servidor disse quanto esperar (A1 do Plano 10): a tela repete.
    if (e.code === 'rate_limited' && e.retryAfter !== null) {
      return `Muitas tentativas. Aguarde ${duracao(e.retryAfter)} e tente de novo.`
    }
    const base = porCodigo[e.code] ?? 'Não foi possível concluir.'
    // O trace_id liga a tela ao log do servidor (spec §9.1).
    if ((e.code === 'internal_error' || e.code === 'unexpected') && e.traceId) {
      return `${base} Código para suporte: ${e.traceId}.`
    }
    return base
  }
  return 'Algo deu errado no painel.'
}

const porMotivo: Record<string, string> = {
  required: 'Obrigatório.',
  invalid: 'Valor inválido.',
  out_of_range: 'Fora do intervalo permitido.',
  too_long: 'Longo demais.',
  too_short: 'Curto demais.',
  forbidden: 'Não permitido.',
  unavailable: 'Indisponível neste servidor.',
}

const CARACTERE_INVISIVEL = 'Tem um caractere que não pode ser mostrado'

export const LIMITE_DA_REGRA = 'Até 256 caracteres.'
export const CONTROLE_NA_REGRA = 'Sem tab, quebra de linha ou outro caractere de controle.'

// Mensagens específicas por campo, quando o motivo sozinho diz pouco.
const porCampo: Record<string, Record<string, string>> = {
  url: {
    forbidden: 'Endereços internos (localhost, redes privadas) não podem ser monitorados.',
    invalid: 'Use uma URL http:// ou https:// completa.',
  },
  headers: { forbidden: 'Há um header controlado pelo monitor (Host, Content-Length…).' },
  tags: {
    invalid: 'Use letras, números, hífen e sublinhado, separados por vírgula.',
    too_long: 'Cada tag tem até 24 caracteres.',
    out_of_range: 'Até 8 tags.',
  },
  description: { invalid: `${CARACTERE_INVISIVEL}.`, out_of_range: 'No máximo 10000 caracteres.' },
  period_sec: { out_of_range: 'Entre 1 minuto e 365 dias.' },
  grace_sec: { out_of_range: 'Entre 30 segundos e 365 dias.' },
  expr: { invalid: 'Use os cinco campos, com um disparo que exista: 0 3 * * *. Domingo é 0 ou SUN, não 7; de sexta a domingo, 5,6,0.' },
  timezone: { invalid: 'Use um fuso IANA, como America/Sao_Paulo.' },
  timeout_ms: { out_of_range: 'Entre 1000 e 30000 ms, e menor que 90% do intervalo.' },
  interval_sec: { out_of_range: 'Entre 30 segundos e 1 dia.' },
  kind: { unavailable: 'O envio de e-mail não está configurado neste servidor.' },
  email: { invalid: 'E-mail inválido.' },
  slug: { invalid: 'De 3 a 40 caracteres: letras minúsculas, números e hífens, sem hífen nas pontas.' },
  new_password: { too_short: 'Mínimo de 15 caracteres.', too_long: 'Máximo de 128 caracteres.' },
  content_rule: { forbidden: 'Para verificar o conteúdo, use GET ou POST e tire o header Accept-Encoding.' },
  'content_rule.value': { too_long: LIMITE_DA_REGRA, invalid: CONTROLE_NA_REGRA },
  'content_rule.json_pointer': {
    too_long: LIMITE_DA_REGRA,
    invalid: 'Comece com /, como /status, sem tab ou quebra de linha. No ponteiro, escreva ~1 para uma barra e ~0 para um til.',
  },
}

export function mensagemDoCampo(campo: string, motivo: string): string {
  const raiz = campo.replace(/\[\d+\].*$/, '')
  return porCampo[raiz]?.[motivo] ?? porMotivo[motivo] ?? 'Valor inválido.'
}

export function mensagemDaLinhaDaDescricao(linha: number): string {
  return `${CARACTERE_INVISIVEL} na linha ${linha}.`
}

// Os textos dos vocabulários do sondador e dos canais.
const tiposDeErro: Record<string, string> = {
  timeout: 'Tempo esgotado',
  dns: 'Falha de DNS',
  tls: 'Erro de TLS',
  conn_refused: 'Conexão recusada',
  http_status: 'Status HTTP inesperado',
  // Só sai com regra de conteúdo: o corpo passou do que o sondador lê.
  body_limit: 'Resposta grande demais para conferir o conteúdo',
  content_mismatch: 'Conteúdo diferente do esperado',
  content_encoding: 'Resposta compactada num formato que o StillOn não lê',
  blocked: 'Destino bloqueado pela proteção de rede',
  heartbeat_late: 'Sem sinal no prazo',
  heartbeat_failed: 'A tarefa avisou falha',
}

export function textoDoErroDeCheck(kind: string | null, statusCode: number | null): string {
  if (!kind) return ''
  const base = tiposDeErro[kind] ?? 'Falha'
  return kind === 'http_status' && statusCode !== null ? `${base} (${statusCode})` : base
}

// A causa de um incidente para a tela. A coluna cause guarda o error_kind
// do check que abriu o incidente; vazia (um dado antigo), vira uma frase, e
// não um espaço em branco entre dois "·".
export function textoDaCausa(cause: string): string {
  return textoDoErroDeCheck(cause, null) || 'Causa não registrada'
}

const errosDeCanal: Record<string, string> = {
  timeout: 'Tempo esgotado',
  network: 'Falha de rede',
  rate_limited: 'O destino limitou os envios (429)',
  rejected: 'O destino recusou',
  server_error: 'Erro no destino (5xx)',
  invalid_channel: 'Canal inválido (o webhook foi apagado?)',
  unknown: 'Erro desconhecido',
}

export function textoDoErroDeCanal(motivo: string): string {
  return errosDeCanal[motivo] ?? 'Erro desconhecido'
}
