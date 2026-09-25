import type { z } from 'zod'
import { enviar, lerCorpo, lerErro, type Buscar, type Pedido } from './http'
import {
  Alvo,
  CadastroTotp,
  Canal,
  ConfirmacaoTotp,
  ListaDeAlvos,
  ListaDeCanais,
  ListaDeIncidentes,
  ListaDePaginas,
  ListaDeSessoes,
  Incidente,
  Me,
  PaginaDeStatus,
  Resumo,
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
}

export type AlteracaoAlvo = Partial<EntradaAlvo>

export type EntradaCanal = { kind: 'discord'; url: string } | { kind: 'email'; email: string }

export type EntradaPagina = {
  slug: string
  title: string
  published: boolean
  items: { target_id: string; display_name: string }[]
}

export type TrocaDeSenha = { current_password: string; new_password: string; totp_code?: string }

export type FiltroIncidentes = { status?: 'open' | 'resolved'; cursor?: string; targetId?: string; limit?: number }

function consulta(p: Record<string, string | number | undefined>) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(p)) if (v !== undefined && v !== '') q.set(k, String(v))
  const s = q.toString()
  return s ? `?${s}` : ''
}

export type Api = ReturnType<typeof criarApi>

export function criarApi(sessao: Sessao, buscar: Buscar) {
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

    resumo: () => get('/targets/summary', Resumo),
    alvos: () => get('/targets?limit=100', ListaDeAlvos),
    alvo: (id: string) => get(`/targets/${encodeURIComponent(id)}`, Alvo),
    criarAlvo: (e: EntradaAlvo) => pedir({ metodo: 'POST', caminho: '/targets', corpo: e }, Alvo),
    alterarAlvo: (id: string, a: AlteracaoAlvo) =>
      pedir({ metodo: 'PATCH', caminho: `/targets/${encodeURIComponent(id)}`, corpo: a }, Alvo),
    apagarAlvo: (id: string) => pedir({ metodo: 'DELETE', caminho: `/targets/${encodeURIComponent(id)}` }, null),
    // Só from: o "agora" é do servidor, sem depender do relógio do navegador.
    serie: (id: string, desde: string) =>
      get(`/targets/${encodeURIComponent(id)}/checks${consulta({ from: desde })}`, Serie),

    incidentes: (f: FiltroIncidentes) =>
      get(
        `/incidents${consulta({ status: f.status, cursor: f.cursor, target_id: f.targetId, limit: f.limit })}`,
        ListaDeIncidentes,
      ),
    reconhecer: (id: string) =>
      pedir({ metodo: 'POST', caminho: `/incidents/${encodeURIComponent(id)}/ack` }, Incidente),

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
      pedir({ metodo: 'POST', caminho: '/auth/totp/confirm', corpo: { code: codigo } }, ConfirmacaoTotp),
    totpDesativar: (senha: string, codigo: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/disable', corpo: { password: senha, code: codigo } }, null),
    // A troca de autenticador (spec do Plano 9 §3.2): as provas do disable
    // (o código do app atual ou um de recuperação) e, depois, o código do
    // app NOVO. As respostas têm o formato do enroll e do confirm.
    totpTrocar: (senha: string, codigo: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/rotate', corpo: { password: senha, code: codigo } }, CadastroTotp),
    totpConfirmarTroca: (codigo: string) =>
      pedir({ metodo: 'POST', caminho: '/auth/totp/rotate/confirm', corpo: { code: codigo } }, ConfirmacaoTotp),
  }
}
