import type { z } from 'zod'
import { caminho } from './caminho'
import { enviar, erroDeRede, lerCorpo, lerErro, type Buscar, type Pedido } from './http'
import {
  Alvo,
  CadastroTotp,
  Convite,
  AgendamentoEditavel,
  Canal,
  ConfirmacaoTotp,
  ConfigDoCadastro,
  Heartbeat,
  HeartbeatComPing,
  IncidenteDeHeartbeat,
  InicioDaTroca,
  ListaDeAlvos,
  ListaDeCanais,
  ListaDeConvites,
  ListaDeEventos,
  ListaDeHeartbeats,
  ListaDeIncidentes,
  ListaDeIncidentesDeHeartbeat,
  ListaDeMembros,
  ListaDePaginas,
  ListaDeProjetos,
  ListaDeSessoes,
  Incidente,
  Me,
  Membro,
  PaginaDeStatus,
  Projeto,
  Resumo,
  PreviaDoCron,
  Serie,
  type RegraDeConteudo,
} from './schemas'
import type { Sessao } from './sessao'
import type { PapelConhecido } from '@/lib/permissoes'

// A regra do 401 (spec do Plano 6 §3.4; nota do Plano 5 §12; reconciliação,
// item 3): só "401 unauthorized" FORA de /auth/* renova, e a requisição
// repete UMA vez com o token novo. Refresh recusado, ou 401 de novo depois
// de renovar, é o fim da sessão.
//
// Em /auth/* um 401 nunca renova. "invalid_credentials" é senha ou código
// errado (em /auth/password, /auth/totp/enroll e /auth/totp/disable, D24 do
// Plano 5; e em /auth/totp/rotate e /auth/totp/rotate/confirm, spec do
// Plano 9 §3.2), e repetir reenviaria o erro e contaria outra falha no
// bloqueio progressivo.
function podeRenovar(rota: string, code: string) {
  return code === 'unauthorized' && !rota.startsWith('/auth/')
}

export type EntradaAlvo = {
  name: string
  url: string
  method: 'GET' | 'HEAD' | 'POST'
  interval_sec: number
  timeout_ms: number
  expected_status: number
  // Ausente = padrão do servidor (2000 ms); null = desativado (spec §11.5).
  degraded_threshold_ms?: number | null
  enabled: boolean
  headers?: Record<string, string> | null
  tags?: string[]
  // Ausente mantém (no PATCH) ou cria sem regra; null limpa.
  content_rule?: EntradaRegra | null
}

// json_pointer só vai com json_equals: nos outros modos o servidor o recusa.
export type EntradaRegra =
  | { mode: Exclude<RegraDeConteudo['mode'], 'json_equals'>; value: string }
  | { mode: 'json_equals'; value: string; json_pointer: string }

export type AlteracaoAlvo = Partial<EntradaAlvo>

export type EntradaHeartbeat = {
  name: string
  schedule: AgendamentoEditavel
  grace_sec: number
  tags?: string[]
  description?: string
  ping_resumes?: boolean
}

export type AlteracaoHeartbeat = Partial<EntradaHeartbeat> & { enabled?: boolean }

export type EntradaCanal = { kind: 'discord'; url: string } | { kind: 'email'; email: string }

export type EntradaPagina = {
  slug: string
  title: string
  published: boolean
  items: { target_id: string; display_name: string }[]
}

export type EntradaCadastro = { email: string; password: string; turnstile_token: string; accepted_terms_version: string }
export type PedidoDeRedefinicao = { email: string; turnstile_token: string }
export type Redefinicao = { token: string; new_password: string }

export type EntradaProjeto = { name: string }

export type EntradaConvite = { email: string; role: PapelConhecido }

export type TrocaDeSenha = { current_password: string; new_password: string; totp_code?: string }

// Os valores do ?state= do resumo: os do servidor, mais o pausado.
export const ESTADOS_DO_FILTRO = ['down', 'degraded', 'up', 'unknown', 'paused'] as const
export type EstadoDoFiltro = (typeof ESTADOS_DO_FILTRO)[number]

export type FiltroDoResumo = { tags: readonly string[]; estados: readonly EstadoDoFiltro[] }

export type FiltroIncidentes = { status?: 'open' | 'resolved'; cursor?: string; targetId?: string; limit?: number }

function consulta(p: Record<string, string | number | undefined>) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(p)) if (v !== undefined && v !== '') q.set(k, String(v))
  const s = q.toString()
  return s ? `?${s}` : ''
}

// Parâmetro repetido, que o consulta() acima não faz: tag=a&tag=b.
function consultaDoResumo({ tags, estados }: FiltroDoResumo): string {
  const q = new URLSearchParams()
  for (const tag of tags) q.append('tag', tag)
  for (const estado of estados) q.append('state', estado)
  const s = q.toString()
  return s ? `?${s}` : ''
}

const LIMITE_DO_LOG = 20
const PRAZO_DA_CONSULTA_MS = 10_000

// As duas formas de pedir que criarApi monta (com a regra do 401) e que os
// recursos de um projeto reaproveitam.
type Pedir = {
  (p: Omit<Pedido, 'token'>, esquema: null): Promise<void>
  <T>(p: Omit<Pedido, 'token'>, esquema: z.ZodType<T>): Promise<T>
}
type Get = <T>(rota: string, esquema: z.ZodType<T>, cancelamento?: AbortSignal) => Promise<T>

// Os métodos dos recursos de um projeto (alvos, incidentes, heartbeats,
// canais e páginas), todos sob /projects/{id}. A conta (me, sessões, senha e
// 2FA) fica fora: não é de projeto.
function criarApiDoProjeto(get: Get, pedir: Pedir, projeto: string) {
  const prefixo = caminho`/projects/${projeto}`
  return {
    resumo: (filtro: FiltroDoResumo = { tags: [], estados: [] }, sinal?: AbortSignal) =>
      get(prefixo + `/targets/summary${consultaDoResumo(filtro)}`, Resumo, sinal),
    alvos: (sinal?: AbortSignal) => get(prefixo + '/targets?limit=100', ListaDeAlvos, sinal),
    alvo: (id: string, sinal?: AbortSignal) => get(prefixo + caminho`/targets/${id}`, Alvo, sinal),
    criarAlvo: (e: EntradaAlvo) => pedir({ metodo: 'POST', caminho: prefixo + '/targets', corpo: e }, Alvo),
    alterarAlvo: (id: string, a: AlteracaoAlvo) =>
      pedir({ metodo: 'PATCH', caminho: prefixo + caminho`/targets/${id}`, corpo: a }, Alvo),
    apagarAlvo: (id: string) => pedir({ metodo: 'DELETE', caminho: prefixo + caminho`/targets/${id}` }, null),
    // Só a janela, pelo nome (B6 do Plano 10): o servidor calcula from = to −
    // janela com o relógio dele. O from calculado aqui dependia do relógio
    // do navegador, e o to sempre foi o do servidor.
    serie: (id: string, janela: string, sinal?: AbortSignal) =>
      get(prefixo + caminho`/targets/${id}/checks` + consulta({ window: janela }), Serie, sinal),

    incidentes: (f: FiltroIncidentes, sinal?: AbortSignal) =>
      get(
        prefixo + `/incidents${consulta({ status: f.status, cursor: f.cursor, target_id: f.targetId, limit: f.limit })}`,
        ListaDeIncidentes,
        sinal,
      ),
    reconhecer: (id: string) =>
      pedir({ metodo: 'POST', caminho: prefixo + caminho`/incidents/${id}/ack` }, Incidente),

    // Uma página de 100 é tudo: é o teto por usuário (spec da tela, T7).
    heartbeats: (sinal?: AbortSignal) => get(prefixo + '/heartbeats?limit=100', ListaDeHeartbeats, sinal),
    heartbeat: (id: string, sinal?: AbortSignal) => get(prefixo + caminho`/heartbeats/${id}`, Heartbeat, sinal),
    previaDoCron: (expr: string, timezone: string, sinal?: AbortSignal) =>
      get(prefixo + `/heartbeats/cron-preview${consulta({ expr, timezone })}`, PreviaDoCron, sinal),
    criarHeartbeat: (e: EntradaHeartbeat) => pedir({ metodo: 'POST', caminho: prefixo + '/heartbeats', corpo: e }, HeartbeatComPing),
    alterarHeartbeat: (id: string, a: AlteracaoHeartbeat) =>
      pedir({ metodo: 'PATCH', caminho: prefixo + caminho`/heartbeats/${id}`, corpo: a }, Heartbeat),
    apagarHeartbeat: (id: string) => pedir({ metodo: 'DELETE', caminho: prefixo + caminho`/heartbeats/${id}` }, null),
    rotacionarHeartbeat: (id: string) =>
      pedir({ metodo: 'POST', caminho: prefixo + caminho`/heartbeats/${id}/token/rotate` }, HeartbeatComPing),
    duplicarHeartbeat: (id: string) =>
      pedir({ metodo: 'POST', caminho: prefixo + caminho`/heartbeats/${id}/clone` }, HeartbeatComPing),
    limparEventosDoHeartbeat: (id: string) =>
      pedir({ metodo: 'POST', caminho: prefixo + caminho`/heartbeats/${id}/events/clear` }, null),
    // Um sucesso pelo painel; a resposta é o heartbeat já atualizado.
    sinalizarAgora: (id: string) =>
      pedir({ metodo: 'POST', caminho: prefixo + caminho`/heartbeats/${id}/ping-now` }, Heartbeat),
    eventosDoHeartbeat: (id: string, cursor?: string, sinal?: AbortSignal) =>
      get(prefixo + caminho`/heartbeats/${id}/events` + consulta({ cursor, limit: LIMITE_DO_LOG }), ListaDeEventos, sinal),
    incidentesDoHeartbeat: (id: string, cursor?: string, sinal?: AbortSignal) =>
      get(prefixo + caminho`/heartbeats/${id}/incidents` + consulta({ cursor, limit: LIMITE_DO_LOG }), ListaDeIncidentesDeHeartbeat, sinal),
    reconhecerDoHeartbeat: (id: string, incidente: string) =>
      pedir(
        { metodo: 'POST', caminho: prefixo + caminho`/heartbeats/${id}/incidents/${incidente}/ack` },
        IncidenteDeHeartbeat,
      ),
    canais: (sinal?: AbortSignal) => get(prefixo + '/channels', ListaDeCanais, sinal),
    criarCanal: (e: EntradaCanal) => pedir({ metodo: 'POST', caminho: prefixo + '/channels', corpo: e }, Canal),
    apagarCanal: (id: string) => pedir({ metodo: 'DELETE', caminho: prefixo + caminho`/channels/${id}` }, null),
    reenviarVerificacao: (id: string) =>
      pedir({ metodo: 'POST', caminho: prefixo + caminho`/channels/${id}/verify/resend` }, null),
    // Spec do Plano 9 §3.3: pausar (enabled false) e retomar (true) pelo
    // mesmo PATCH; a resposta é o canal no formato da lista.
    pausarCanal: (id: string, enabled: boolean) =>
      pedir({ metodo: 'PATCH', caminho: prefixo + caminho`/channels/${id}`, corpo: { enabled } }, Canal),

    paginas: (sinal?: AbortSignal) => get(prefixo + '/status-pages', ListaDePaginas, sinal),
    criarPagina: (e: EntradaPagina) => pedir({ metodo: 'POST', caminho: prefixo + '/status-pages', corpo: e }, PaginaDeStatus),
    alterarPagina: (id: string, e: Partial<EntradaPagina>) =>
      pedir({ metodo: 'PATCH', caminho: prefixo + caminho`/status-pages/${id}`, corpo: e }, PaginaDeStatus),

    // A equipe (PR 4). Remover o próprio id é sair do projeto.
    membros: (sinal?: AbortSignal) => get(prefixo + '/members', ListaDeMembros, sinal),
    mudarPapel: (userId: string, role: PapelConhecido) =>
      pedir({ metodo: 'PATCH', caminho: prefixo + caminho`/members/${userId}`, corpo: { role } }, Membro),
    removerMembro: (userId: string) => pedir({ metodo: 'DELETE', caminho: prefixo + caminho`/members/${userId}` }, null),
    renomear: (nome: string) => pedir({ metodo: 'PATCH', caminho: prefixo, corpo: { name: nome } }, Projeto),

    // Member e viewer recebem 403 na lista de convites.
    convites: (sinal?: AbortSignal) => get(prefixo + '/invitations', ListaDeConvites, sinal),
    convidar: (e: EntradaConvite) => pedir({ metodo: 'POST', caminho: prefixo + '/invitations', corpo: e }, Convite),
    revogarConvite: (id: string) => pedir({ metodo: 'DELETE', caminho: prefixo + caminho`/invitations/${id}` }, null),

    // A titularidade: propor responde 202 sem corpo (só muda com o aceite).
    proporTransferencia: (userId: string) =>
      pedir({ metodo: 'POST', caminho: prefixo + '/titular-transfer', corpo: { user_id: userId } }, null),
    aceitarTransferencia: () => pedir({ metodo: 'POST', caminho: prefixo + '/titular-transfer/accept' }, Projeto),
    cancelarTransferencia: () => pedir({ metodo: 'DELETE', caminho: prefixo + '/titular-transfer' }, null),
  }
}

export type ApiDoProjeto = ReturnType<typeof criarApiDoProjeto>

export type Api = ReturnType<typeof criarApi>

// O prazo dos confirms do 2FA (E3 do Plano 10). O diálogo trava enquanto o
// confirm voa, e sem prazo uma rede presa o prendia sem saída; estourado, o
// painel diz que não sabe se o servidor gravou (a resposta se perdeu).
export const PRAZO_DO_CONFIRM = 30_000

export type OpcoesDaApi = { prazoDoConfirmMs?: number }

export function criarApi(sessao: Sessao, buscar: Buscar, o: OpcoesDaApi = {}) {
  const prazoDoConfirm = () => AbortSignal.timeout(o.prazoDoConfirmMs ?? PRAZO_DO_CONFIRM)
  // Sem esquema não há corpo a devolver: as sobrecargas dizem isso no tipo, e o
  // `undefined as T` que fingia um T qualquer deixa de existir.
  function pedir(p: Omit<Pedido, 'token'>, esquema: null): Promise<void>
  function pedir<T>(p: Omit<Pedido, 'token'>, esquema: z.ZodType<T>): Promise<T>
  async function pedir<T>(p: Omit<Pedido, 'token'>, esquema: z.ZodType<T> | null): Promise<T | undefined> {
    const sinalDaConsulta = p.metodo === 'GET' ? p.sinal : undefined
    const usado = sessao.token()
    let r = await enviar(buscar, { ...p, token: usado })
    sinalDaConsulta?.throwIfAborted()
    if (r.status === 401) {
      const erro = await lerErro(r)
      sinalDaConsulta?.throwIfAborted()
      // Sem token não há o que renovar: é uma requisição que ainda voava
      // quando a sessão acabou. Renovar aqui rodaria um refresh com o cookie
      // já apagado e trocaria o motivo do fim da sessão.
      if (usado === null || !podeRenovar(p.caminho, erro.code)) throw erro
      const novo = await sessao.renovar(usado)
      sinalDaConsulta?.throwIfAborted()
      if (!novo) {
        // A sessão pode já ter acabado por outro motivo enquanto o refresh
        // voava (sair, encerrar aqui): o motivo que vale é o primeiro.
        if (sessao.estado() === 'autenticado') sessao.encerrar('expirou')
        throw erro
      }
      r = await enviar(buscar, { ...p, token: novo })
      sinalDaConsulta?.throwIfAborted()
      if (r.status === 401) {
        const outro = await lerErro(r)
        sinalDaConsulta?.throwIfAborted()
        // Como acima: se a sessão acabou enquanto a repetição voava, o
        // motivo que vale é o primeiro.
        if (sessao.estado() === 'autenticado' && podeRenovar(p.caminho, outro.code)) sessao.encerrar('expirou')
        throw outro
      }
    }
    if (!r.ok) throw await lerErro(r)
    if (esquema === null) return undefined
    // Com esquema, um 204 ou um corpo vazio não é um T: o lerCorpo falha com o
    // ApiError 'unexpected' de sempre, em vez de devolver undefined com tipo T.
    return lerCorpo(r, esquema)
  }

  // As rotas públicas do cadastro dispensam o token de acesso e os cookies
  // de sessão. Um 401 volta ao chamador sem renovar a sessão.
  function publico(p: Omit<Pedido, 'token'>, esquema: null): Promise<void>
  function publico<T>(p: Omit<Pedido, 'token'>, esquema: z.ZodType<T>): Promise<T>
  async function publico<T>(p: Omit<Pedido, 'token'>, esquema: z.ZodType<T> | null): Promise<T | undefined> {
    // O GET já chega com o prazo do get(); as mutações ganham o mesmo prazo
    // do login, para uma rede presa não deixar o formulário sem resposta.
    const r = await enviar(buscar, { ...p, sinal: p.sinal ?? AbortSignal.timeout(PRAZO_DA_CONSULTA_MS) }, 'omit')
    if (!r.ok) throw await lerErro(r)
    return esquema === null ? undefined : lerCorpo(r, esquema)
  }

  // O GET com prazo e cancelamento; quem envia é o pedir (com sessão) ou o
  // publico (sem token).
  async function get<T>(
    rota: string,
    esquema: z.ZodType<T>,
    cancelamento?: AbortSignal,
    enviarPedido: (p: Omit<Pedido, 'token'>, esquema: z.ZodType<T>) => Promise<T> = pedir,
  ): Promise<T> {
    if (cancelamento?.aborted) throw cancelamento.reason
    const controlador = new AbortController()
    // O prazo de rede usa o relógio nativo, como a sessão. Ele é
    // independente dos timers de atualização do painel.
    const prazo = AbortSignal.timeout(PRAZO_DA_CONSULTA_MS)

    // O fetch e o corpo usam o sinal. A consulta também pode deixar de
    // esperar pelo refresh compartilhado, que tem prazo próprio. O mesmo
    // controlador reúne os dois motivos de aborto nos navegadores alvo.
    let cancelar!: (motivo: unknown) => void
    const cancelada = new Promise<never>((_, rejeitar) => {
      cancelar = (motivo) => {
        rejeitar(motivo)
        controlador.abort(motivo)
      }
    })
    const abortarPeloChamador = () => cancelar(cancelamento?.reason)
    const abortarPorPrazo = () => cancelar(erroDeRede())
    cancelamento?.addEventListener('abort', abortarPeloChamador, { once: true })
    prazo.addEventListener('abort', abortarPorPrazo, { once: true })
    try {
      return await Promise.race([
        enviarPedido({ metodo: 'GET', caminho: rota, sinal: controlador.signal }, esquema),
        cancelada,
      ])
    } finally {
      prazo.removeEventListener('abort', abortarPorPrazo)
      cancelamento?.removeEventListener('abort', abortarPeloChamador)
    }
  }

  return {
    me: (sinal?: AbortSignal) => get('/me', Me, sinal),

    // Cadastro público (PR 3). O resend é o único com sessão: reenvia a
    // confirmação para o e-mail da conta logada.
    // O tipo explícito: com o publico (sobrecarregado) como enviarPedido, a
    // inferência do get caía em unknown.
    configDoCadastro: (sinal?: AbortSignal) =>
      get<ConfigDoCadastro>('/auth/signup-config', ConfigDoCadastro, sinal, publico),
    cadastrar: (e: EntradaCadastro) => publico({ metodo: 'POST', caminho: '/auth/signup', corpo: e }, null),
    // A senha do cadastro: o link sozinho não habilita a conta.
    confirmarEmail: (token: string, senha: string) =>
      publico({ metodo: 'POST', caminho: '/auth/email/confirm', corpo: { token, password: senha } }, null),
    reenviarConfirmacao: () => pedir({ metodo: 'POST', caminho: '/auth/email/resend' }, null),
    pedirRedefinicao: (e: PedidoDeRedefinicao) =>
      publico({ metodo: 'POST', caminho: '/auth/password/forgot', corpo: e }, null),
    redefinirSenha: (e: Redefinicao) => publico({ metodo: 'POST', caminho: '/auth/password/reset', corpo: e }, null),

    projetos: (sinal?: AbortSignal) => get('/projects', ListaDeProjetos, sinal),
    criarProjeto: (e: EntradaProjeto) => pedir({ metodo: 'POST', caminho: '/projects', corpo: e }, Projeto),
    doProjeto: (id: string): ApiDoProjeto => criarApiDoProjeto(get, pedir, id),
    // O aceite é da conta: o projeto vem do convite do token.
    aceitarConvite: (token: string) =>
      pedir({ metodo: 'POST', caminho: '/invitations/accept', corpo: { token } }, Projeto),

    sessoes: (sinal?: AbortSignal) => get('/sessions', ListaDeSessoes, sinal),
    encerrarSessao: (id: string) => pedir({ metodo: 'DELETE', caminho: caminho`/sessions/${id}` }, null),
    trocarSenha: (t: TrocaDeSenha) => pedir({ metodo: 'POST', caminho: '/auth/password', corpo: t }, null),

    totpCadastrar: (senha: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/enroll', corpo: { password: senha } }, CadastroTotp),
    totpConfirmar: (codigo: string) =>
      pedir(
        { metodo: 'POST', caminho: '/auth/totp/confirm', corpo: { code: codigo }, sinal: prazoDoConfirm() },
        ConfirmacaoTotp,
      ),
    totpDesativar: (senha: string, codigo: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/disable', corpo: { password: senha, code: codigo } }, null),
    // A troca de autenticador (spec do Plano 9 §3.2): as provas do disable
    // (o código do app atual ou um de recuperação) e, depois, o código do
    // app NOVO com o rotation que o rotate devolveu (A7 do Plano 10). As
    // respostas têm o formato do enroll (mais o rotation) e do confirm.
    // O rotate tem o prazo dos confirms (I1 da revisão do Plano 10): o
    // passo 1 trava o diálogo enquanto ele voa (E1), e sem prazo a rede presa
    // deixava o diálogo travado para sempre.
    totpTrocar: (senha: string, codigo: string) =>
      pedir(
        {
          metodo: 'POST',
          caminho: '/auth/totp/rotate',
          corpo: { password: senha, code: codigo },
          sinal: prazoDoConfirm(),
        },
        InicioDaTroca,
      ),
    totpConfirmarTroca: (codigo: string, rotation: string) =>
      pedir(
        {
          metodo: 'POST',
          caminho: '/auth/totp/rotate/confirm',
          corpo: { code: codigo, rotation },
          sinal: prazoDoConfirm(),
        },
        ConfirmacaoTotp,
      ),
  }
}
