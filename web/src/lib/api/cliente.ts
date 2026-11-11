import type { z } from 'zod'
import { enviar, lerCorpo, lerErro, type Buscar, type Pedido } from './http'
import {
  Alvo,
  CadastroTotp,
  AgendamentoEditavel,
  Canal,
  ConfirmacaoTotp,
  Heartbeat,
  HeartbeatComPing,
  IncidenteDeHeartbeat,
  InicioDaTroca,
  ListaDeAlvos,
  ListaDeCanais,
  ListaDeEventos,
  ListaDeHeartbeats,
  ListaDeIncidentes,
  ListaDeIncidentesDeHeartbeat,
  ListaDePaginas,
  ListaDeSessoes,
  Incidente,
  Me,
  PaginaDeStatus,
  Resumo,
  PreviaDoCron,
  Serie,
} from './schemas'
import type { Sessao } from './sessao'

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
function podeRenovar(caminho: string, code: string) {
  return code === 'unauthorized' && !caminho.startsWith('/auth/')
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
}

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

export type Api = ReturnType<typeof criarApi>

// O prazo dos confirms do 2FA (E3 do Plano 10). O diálogo trava enquanto o
// confirm voa, e sem prazo uma rede presa o prendia sem saída; estourado, o
// painel diz que não sabe se o servidor gravou (a resposta se perdeu).
export const PRAZO_DO_CONFIRM = 30_000

export type OpcoesDaApi = { prazoDoConfirmMs?: number }

export function criarApi(sessao: Sessao, buscar: Buscar, o: OpcoesDaApi = {}) {
  const prazoDoConfirm = () => AbortSignal.timeout(o.prazoDoConfirmMs ?? PRAZO_DO_CONFIRM)
  async function pedir<T>(p: Omit<Pedido, 'token'>, esquema: z.ZodType<T> | null): Promise<T> {
    const usado = sessao.token()
    let r = await enviar(buscar, { ...p, token: usado })
    if (r.status === 401) {
      const erro = await lerErro(r)
      // Sem token não há o que renovar: é uma requisição que ainda voava
      // quando a sessão acabou. Renovar aqui rodaria um refresh com o cookie
      // já apagado e trocaria o motivo do fim da sessão.
      if (usado === null || !podeRenovar(p.caminho, erro.code)) throw erro
      const novo = await sessao.renovar(usado)
      if (!novo) {
        // A sessão pode já ter acabado por outro motivo enquanto o refresh
        // voava (sair, encerrar aqui): o motivo que vale é o primeiro.
        if (sessao.estado() === 'autenticado') sessao.encerrar('expirou')
        throw erro
      }
      r = await enviar(buscar, { ...p, token: novo })
      if (r.status === 401) {
        const outro = await lerErro(r)
        // Como acima: se a sessão acabou enquanto a repetição voava, o
        // motivo que vale é o primeiro.
        if (sessao.estado() === 'autenticado' && podeRenovar(p.caminho, outro.code)) sessao.encerrar('expirou')
        throw outro
      }
    }
    if (!r.ok) throw await lerErro(r)
    if (esquema === null || r.status === 204) return undefined as T
    return lerCorpo(r, esquema)
  }

  const get = <T>(caminho: string, esquema: z.ZodType<T>) => pedir({ metodo: 'GET', caminho }, esquema)

  return {
    me: () => get('/me', Me),

    resumo: (filtro: FiltroDoResumo = { tags: [], estados: [] }) => get(`/targets/summary${consultaDoResumo(filtro)}`, Resumo),
    alvos: () => get('/targets?limit=100', ListaDeAlvos),
    alvo: (id: string) => get(`/targets/${encodeURIComponent(id)}`, Alvo),
    criarAlvo: (e: EntradaAlvo) => pedir({ metodo: 'POST', caminho: '/targets', corpo: e }, Alvo),
    alterarAlvo: (id: string, a: AlteracaoAlvo) =>
      pedir({ metodo: 'PATCH', caminho: `/targets/${encodeURIComponent(id)}`, corpo: a }, Alvo),
    apagarAlvo: (id: string) => pedir({ metodo: 'DELETE', caminho: `/targets/${encodeURIComponent(id)}` }, null),
    // Só a janela, pelo nome (B6 do Plano 10): o servidor calcula from = to −
    // janela com o relógio dele. O from calculado aqui dependia do relógio
    // do navegador, e o to sempre foi o do servidor.
    serie: (id: string, janela: string) =>
      get(`/targets/${encodeURIComponent(id)}/checks${consulta({ window: janela })}`, Serie),

    incidentes: (f: FiltroIncidentes) =>
      get(
        `/incidents${consulta({ status: f.status, cursor: f.cursor, target_id: f.targetId, limit: f.limit })}`,
        ListaDeIncidentes,
      ),
    reconhecer: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/incidents/${encodeURIComponent(id)}/ack` }, Incidente),

    // Uma página de 100 é tudo: é o teto por usuário (spec da tela, T7).
    heartbeats: () => get('/heartbeats?limit=100', ListaDeHeartbeats),
    heartbeat: (id: string) => get(`/heartbeats/${encodeURIComponent(id)}`, Heartbeat),
    previaDoCron: (expr: string, timezone: string) => get(`/heartbeats/cron-preview${consulta({ expr, timezone })}`, PreviaDoCron),
    criarHeartbeat: (e: EntradaHeartbeat) => pedir({ metodo: 'POST', caminho: '/heartbeats', corpo: e }, HeartbeatComPing),
    alterarHeartbeat: (id: string, a: AlteracaoHeartbeat) =>
      pedir({ metodo: 'PATCH', caminho: `/heartbeats/${encodeURIComponent(id)}`, corpo: a }, Heartbeat),
    apagarHeartbeat: (id: string) => pedir({ metodo: 'DELETE', caminho: `/heartbeats/${encodeURIComponent(id)}` }, null),
    rotacionarHeartbeat: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/heartbeats/${encodeURIComponent(id)}/token/rotate` }, HeartbeatComPing),
    duplicarHeartbeat: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/heartbeats/${encodeURIComponent(id)}/clone` }, HeartbeatComPing),
    limparEventosDoHeartbeat: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/heartbeats/${encodeURIComponent(id)}/events/clear` }, null),
    // Um sucesso pelo painel; a resposta é o heartbeat já atualizado.
    sinalizarAgora: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/heartbeats/${encodeURIComponent(id)}/ping-now` }, Heartbeat),
    eventosDoHeartbeat: (id: string, cursor?: string) =>
      get(`/heartbeats/${encodeURIComponent(id)}/events${consulta({ cursor, limit: LIMITE_DO_LOG })}`, ListaDeEventos),
    incidentesDoHeartbeat: (id: string, cursor?: string) =>
      get(`/heartbeats/${encodeURIComponent(id)}/incidents${consulta({ cursor, limit: LIMITE_DO_LOG })}`, ListaDeIncidentesDeHeartbeat),
    reconhecerDoHeartbeat: (id: string, incidente: string) =>
      pedir(
        { metodo: 'POST', caminho: `/heartbeats/${encodeURIComponent(id)}/incidents/${encodeURIComponent(incidente)}/ack` },
        IncidenteDeHeartbeat,
      ),
    canais: () => get('/channels', ListaDeCanais),
    criarCanal: (e: EntradaCanal) => pedir({ metodo: 'POST', caminho: '/channels', corpo: e }, Canal),
    apagarCanal: (id: string) => pedir({ metodo: 'DELETE', caminho: `/channels/${encodeURIComponent(id)}` }, null),
    reenviarVerificacao: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/channels/${encodeURIComponent(id)}/verify/resend` }, null),
    // Spec do Plano 9 §3.3: pausar (enabled false) e retomar (true) pelo
    // mesmo PATCH; a resposta é o canal no formato da lista.
    pausarCanal: (id: string, enabled: boolean) =>
      pedir({ metodo: 'PATCH', caminho: `/channels/${encodeURIComponent(id)}`, corpo: { enabled } }, Canal),

    paginas: () => get('/status-pages', ListaDePaginas),
    criarPagina: (e: EntradaPagina) => pedir({ metodo: 'POST', caminho: '/status-pages', corpo: e }, PaginaDeStatus),
    alterarPagina: (id: string, e: Partial<EntradaPagina>) =>
      pedir({ metodo: 'PATCH', caminho: `/status-pages/${encodeURIComponent(id)}`, corpo: e }, PaginaDeStatus),

    sessoes: () => get('/sessions', ListaDeSessoes),
    encerrarSessao: (id: string) => pedir({ metodo: 'DELETE', caminho: `/sessions/${encodeURIComponent(id)}` }, null),
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
