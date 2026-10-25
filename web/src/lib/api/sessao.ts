import { ApiError, enviar, lerCorpo, lerErro, type Buscar } from './http'
import { RespostaLogin, Token } from './schemas'

// A sessão do painel (spec §11.2; spec do Plano 6 §3.4).
//
// O access token vive numa variável deste módulo e em nenhum outro lugar:
// nada em localStorage, sessionStorage, IndexedDB ou estado React (spec
// §5.3). O refresh é um cookie HttpOnly que o JavaScript não lê; o boot o usa
// para re-hidratar a sessão depois de um F5.
//
// O refresh tem três camadas, e cada uma fecha um jeito de a sessão cair:
// 1. single-flight dentro da aba: cinco 401 juntos esperam UMA promessa;
// 2. Web Locks entre abas: abas do mesmo navegador dividem o cookie, e duas
//    renovando juntas apresentariam o mesmo refresh — a segunda é reúso, e o
//    servidor revoga a família inteira (spec §5.2). Com a trava, a segunda
//    só envia depois de o navegador guardar o cookie novo;
// 3. BroadcastChannel só com eventos, nunca com o token: "encerrada" faz as
//    outras abas largarem o token e atualizarem a tela, sem esperar que a
//    próxima requisição seja recusada pelo servidor.

export type Estado = 'verificando' | 'autenticado' | 'anonimo' | 'offline'
export type Motivo = 'saiu' | 'expirou' | 'encerrada-aqui' | 'outra-aba'

export type Travar = <T>(nome: string, fn: () => Promise<T>) => Promise<T>

export type Canal = {
  postMessage(msg: unknown): void
  addEventListener(tipo: 'message', fn: (e: MessageEvent) => void): void
}

export type Opcoes = {
  buscar: Buscar
  travar?: Travar
  canal?: Canal | null
  // Quanto um refresh (ou o logout) espera antes de desistir. Uma rede
  // travada não pode segurar a trava de todas as abas, nem o "Sair".
  prazoMs?: number
}

export type ResultadoLogin = { tipo: 'ok' } | { tipo: 'totp'; desafio: string }

type Aviso = { tipo: 'encerrada' } | { tipo: 'iniciada' }

// O que chega pelo BroadcastChannel vem de outra aba (de outra versão do
// painel, talvez): entra como unknown, e o que não é um Aviso é ignorado.
function avisoDe(dado: unknown): Aviso | null {
  if (typeof dado !== 'object' || dado === null) return null
  const tipo: unknown = Reflect.get(dado, 'tipo')
  return tipo === 'encerrada' || tipo === 'iniciada' ? { tipo } : null
}

const TRAVA = 'stillon:refresh'

export function travaDoNavegador(): Travar {
  if (typeof navigator !== 'undefined' && navigator.locks) {
    return (nome, fn) => navigator.locks.request(nome, fn)
  }
  return (_nome, fn) => fn()
}

export function canalDoNavegador(): Canal | null {
  return typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('stillon:sessao')
}

export type Sessao = ReturnType<typeof criarSessao>

export function criarSessao(o: Opcoes) {
  const travar = o.travar ?? travaDoNavegador()
  const canal = o.canal ?? null
  const prazoMs = o.prazoMs ?? 10_000

  let token: string | null = null
  let estado: Estado = 'verificando'
  let motivo: Motivo | null = null
  let emVoo: Promise<string | null> | null = null
  let boot: Promise<void> | null = null
  // epoca muda a cada login e a cada fim de sessão. Um refresh que saiu
  // numa época e voltou em outra não mexe no token: sem isto, sair (ou
  // encerrar a sessão aqui) com um refresh em voo ressuscitava o token
  // quando a resposta chegasse.
  let epoca = 0
  const ouvintes = new Set<() => void>()

  function mudou() {
    for (const f of ouvintes) f()
  }

  function avisar(a: Aviso) {
    canal?.postMessage(a)
  }

  // Um refresh de verdade. null = o servidor recusou a sessão (401); erro de
  // rede ou 5xx sobe como ApiError, porque a sessão pode estar boa.
  //
  // minha é a época em que o refresh foi PEDIDO, antes da trava: capturada
  // aqui dentro, depois de a trava soltar, ela já seria a época nova, e um
  // refresh que esperou a outra aba enquanto a sessão acabava gravaria o
  // token (D15).
  async function renovarAgora(minha: number): Promise<string | null> {
    const r = await enviar(o.buscar, {
      metodo: 'POST',
      caminho: '/auth/refresh',
      sinal: AbortSignal.timeout(prazoMs),
    })
    let novo: string | null = null
    if (r.status !== 401) {
      if (!r.ok) throw await lerErro(r)
      novo = (await lerCorpo(r, Token)).access_token
    }
    // Outra época: vale o que ela deixou (null depois de encerrar; o token
    // do login novo depois de entrar).
    if (minha !== epoca) return token
    if (novo !== null) token = novo
    return novo
  }

  // renovar é o que o cliente chama num 401. usado é o token com que a
  // requisição saiu: se outra requisição já renovou enquanto esta voava, não
  // há por que renovar de novo — basta repetir com o token atual.
  //
  // Se a requisição saiu com um token e agora não há token nenhum, a sessão
  // acabou depois de ela sair (sair, encerrar, outra aba): renovar aqui
  // ressuscitaria a sessão se o logout não chegou ao servidor. Só o boot
  // (usado === null) renova sem token.
  function renovar(usado: string | null): Promise<string | null> {
    if (token !== null && token !== usado) return Promise.resolve(token)
    if (usado !== null && token === null) return Promise.resolve(null)
    const minha = epoca
    emVoo ??= travar(TRAVA, () => renovarAgora(minha)).finally(() => {
      emVoo = null
    })
    return emVoo
  }

  function aceitar(novo: string) {
    epoca++
    token = novo
    estado = 'autenticado'
    motivo = null
    mudou()
    avisar({ tipo: 'iniciada' })
  }

  // encerrar larga a sessão deste lado: token, estado e, pelo canal, as
  // outras abas. Não chama o servidor: quem precisa (sair) chama antes.
  function encerrar(m: Motivo) {
    const avisarOutras = m !== 'outra-aba' && estado === 'autenticado'
    epoca++
    token = null
    estado = 'anonimo'
    motivo = m
    mudou()
    if (avisarOutras) avisar({ tipo: 'encerrada' })
  }

  // O boot é UMA promessa por carga de página, fora da árvore React: o
  // StrictMode monta os efeitos duas vezes, e dois refreshes com o mesmo
  // cookie seriam reúso (spec do Plano 6, D9).
  function iniciar(): Promise<void> {
    boot ??= (async () => {
      const minha = epoca
      let novo: Estado
      try {
        const t = await renovar(null)
        novo = t ? 'autenticado' : 'anonimo'
      } catch {
        novo = 'offline'
      }
      // Outra época: a sessão mudou durante o boot (encerrada em outra aba,
      // ou um login nesta), e vale o estado que essa mudança deixou.
      if (minha === epoca) estado = novo
      mudou()
    })()
    return boot
  }

  function tentarDeNovo(): Promise<void> {
    boot = null
    estado = 'verificando'
    mudou()
    return iniciar()
  }

  canal?.addEventListener('message', (e) => {
    const a = avisoDe(e.data)
    // Também no boot: um refresh que o servidor aceitou antes do logout da
    // outra aba entraria numa sessão que o dono acabou de encerrar. A época
    // nova descarta a resposta dele, e o boot termina anônimo.
    if (a?.tipo === 'encerrada' && (estado === 'autenticado' || estado === 'verificando')) encerrar('outra-aba')
    // Outra aba entrou: uma aba parada no /login entra junto, pelo cookie.
    if (a?.tipo === 'iniciada' && estado === 'anonimo') void tentarDeNovo()
  })

  async function entrar(email: string, senha: string): Promise<ResultadoLogin> {
    const r = await enviar(o.buscar, { metodo: 'POST', caminho: '/auth/login', corpo: { email, password: senha } })
    if (!r.ok) throw await lerErro(r)
    const d = await lerCorpo(r, RespostaLogin)
    if ('totp_required' in d) return { tipo: 'totp', desafio: d.challenge }
    aceitar(d.access_token)
    return { tipo: 'ok' }
  }

  async function verificarTotp(desafio: string, codigo: string): Promise<void> {
    const r = await enviar(o.buscar, {
      metodo: 'POST',
      caminho: '/auth/totp/verify',
      corpo: { challenge: desafio, code: codigo },
    })
    if (!r.ok) throw await lerErro(r)
    aceitar((await lerCorpo(r, Token)).access_token)
  }

  async function sair(): Promise<void> {
    let r: Response | null = null
    try {
      // Com prazo: uma rede presa não pode deixar o "Sair" sem resposta. O
      // prazo vencido chega como ApiError 'network' (enviar só repassa o
      // AbortError, e o do prazo é um TimeoutError).
      r = await enviar(o.buscar, { metodo: 'POST', caminho: '/auth/logout', sinal: AbortSignal.timeout(prazoMs) })
    } catch (e) {
      // Sem rede, o logout no servidor não acontece, mas a aba larga a
      // sessão do mesmo jeito: o refresh que ficou vence sozinho.
      if (!(e instanceof ApiError)) throw e
    }
    // A9 do Plano 10: o servidor que não conseguiu revogar a sessão responde
    // 500 e não apaga o cookie, para o cliente tentar de novo. Largar a
    // sessão aqui seria fingir que saiu: ela continuaria viva no servidor.
    if (r && !r.ok) throw await lerErro(r)
    encerrar('saiu')
  }

  return {
    token: () => token,
    estado: () => estado,
    motivo: () => motivo,
    assinar(f: () => void) {
      ouvintes.add(f)
      return () => {
        ouvintes.delete(f)
      }
    },
    iniciar,
    tentarDeNovo,
    renovar,
    entrar,
    verificarTotp,
    sair,
    encerrar,
  }
}
