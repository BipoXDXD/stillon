import { describe, expect, it, vi } from 'vitest'
import { EMAIL, RECUPERACAO_OK, SENHA, TOTP_OK } from '@/test/api-falsa'
import { buscarDoTeste, novaSessao, travaFalsa, usarApiFalsa } from '@/test/ambiente'
import { ApiError } from './http'
import type { Canal } from './sessao'

// Dois BroadcastChannel do mesmo nome, como duas abas.
function canais(): [Canal, Canal] {
  const a = new BroadcastChannel('teste-sessao')
  const b = new BroadcastChannel('teste-sessao')
  return [a, b]
}

// Prova de ausência sem relógio: a e b são o mesmo canal visto de duas
// abas, e mensagens do mesmo emissor chegam em ordem. Depois do que o teste
// quer ver ausente, a posta uma sentinela; quando ela chega em b, tudo o que
// a postou antes já chegou. Uma espera fixa passaria com a mensagem ainda a
// caminho.
async function ateASentinela(a: Canal, recebidos: unknown[]) {
  a.postMessage({ tipo: 'sentinela' })
  await vi.waitFor(() => expect(recebidos.at(-1)).toEqual({ tipo: 'sentinela' }))
  return recebidos.slice(0, -1)
}

describe('boot', () => {
  it('com o cookie de refresh, entra sem pedir a senha', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const { sessao } = novaSessao()
    await sessao.iniciar()
    expect(sessao.estado()).toBe('autenticado')
    expect(sessao.token()).not.toBeNull()
  })

  it('sem cookie, fica anônimo, sem avisar as outras abas', async () => {
    usarApiFalsa()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const { sessao } = novaSessao({ canal: a })
    await sessao.iniciar()
    expect(sessao.estado()).toBe('anonimo')
    // Sabotagem prevista: em iniciar(), avisar({ tipo: 'encerrada' }) também
    // no anônimo → a sentinela chega depois dele.
    expect(await ateASentinela(a, recebidos)).toEqual([])
  })

  it('servidor fora do ar é offline, não anônimo: a sessão pode estar boa', async () => {
    const falsa = usarApiFalsa()
    falsa.estado.forcar.set('POST /auth/refresh', { status: 503, code: 'unavailable' })
    const { sessao } = novaSessao()
    await sessao.iniciar()
    expect(sessao.estado()).toBe('offline')
  })

  // O StrictMode monta os efeitos duas vezes; o boot é uma promessa só.
  // Duas guardas cobrem esta propriedade: o boot único e o single-flight de
  // renovar(). O teste prova a propriedade, não cada guarda: a sabotagem
  // que o deixa vermelho é tirar as duas ("boot ??=" → "boot =" e
  // "emVoo ??=" → "emVoo ="); tirar só uma fica verde.
  it('chamado duas vezes, renova uma vez só', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const { sessao } = novaSessao()
    await Promise.all([sessao.iniciar(), sessao.iniciar()])
    await sessao.iniciar()
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.estado.familiaRevogada).toBe(false)
  })
})

describe('renovar', () => {
  // Uma requisição que saiu com o token antigo e voltou 401 DEPOIS de outra
  // já ter renovado não renova de novo: só repete com o token atual.
  // Sabotagem prevista: em renovar(), trocar
  // "if (token !== null && token !== usado)" por
  // "if (token !== null && token !== usado && Math.random() > 2)".
  it('o 401 atrasado de um token antigo não dispara um segundo refresh', async () => {
    const falsa = usarApiFalsa()
    const { sessao } = novaSessao()
    await sessao.entrar(EMAIL, SENHA)
    const antigo = sessao.token()
    const novo = await sessao.renovar(antigo)
    expect(novo).not.toBe(antigo)
    expect(await sessao.renovar(antigo)).toBe(novo)
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
  })
})

describe('login', () => {
  it('senha certa sem 2FA abre a sessão', async () => {
    usarApiFalsa()
    const { sessao } = novaSessao()
    expect(await sessao.entrar(EMAIL, SENHA)).toEqual({ tipo: 'ok' })
    expect(sessao.estado()).toBe('autenticado')
  })

  it('senha errada é invalid_credentials e não abre nada', async () => {
    usarApiFalsa()
    const { sessao } = novaSessao()
    await expect(sessao.entrar(EMAIL, 'errada')).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(sessao.token()).toBeNull()
  })

  it('com 2FA, a senha devolve o desafio; o código do app ou o de recuperação abrem a sessão', async () => {
    const falsa = usarApiFalsa()
    falsa.estado.totpLigado = true
    for (const codigo of [TOTP_OK, RECUPERACAO_OK]) {
      const { sessao } = novaSessao()
      const r = await sessao.entrar(EMAIL, SENHA)
      expect(r).toEqual({ tipo: 'totp', desafio: 'desafio-1' })
      expect(sessao.token()).toBeNull()
      await sessao.verificarTotp('desafio-1', codigo)
      expect(sessao.estado()).toBe('autenticado')
    }
  })

  it('toda mutação de autenticação leva X-Requested-With (a API falsa recusa sem ele)', async () => {
    const falsa = usarApiFalsa()
    const { sessao } = novaSessao()
    await sessao.entrar(EMAIL, SENHA)
    await sessao.sair()
    expect(falsa.chamadas('POST /auth/login')).toBe(1)
    expect(falsa.chamadas('POST /auth/logout')).toBe(1)
    expect(sessao.estado()).toBe('anonimo')
  })
})

describe('várias abas', () => {
  // Duas abas (duas sessões) dividem o pote de cookies e a trava do
  // navegador. O refresh demora 20 ms, o que garante a sobreposição.
  // Sabotagem prevista: travaFalsa() → (_n, fn) => fn() deixa este teste
  // vermelho (família revogada), como no spike com o navegador de verdade.
  it('duas abas renovando juntas rotacionam em série, e a família sobrevive', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    falsa.estado.atrasoRefresh = 20
    const trava = travaFalsa()
    const aba1 = novaSessao({ travar: trava })
    const aba2 = novaSessao({ travar: trava })
    await Promise.all([aba1.sessao.iniciar(), aba2.sessao.iniciar()])
    expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
    expect(falsa.estado.familiaRevogada).toBe(false)
    expect(aba1.sessao.estado()).toBe('autenticado')
    expect(aba2.sessao.estado()).toBe('autenticado')
  })

  it('sem trava entre abas, a detecção de reúso derruba a família (o defeito que a trava fecha)', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    falsa.estado.atrasoRefresh = 20
    const semTrava = <T,>(_n: string, fn: () => Promise<T>) => fn()
    const aba1 = novaSessao({ travar: semTrava })
    const aba2 = novaSessao({ travar: semTrava })
    await Promise.all([aba1.sessao.iniciar(), aba2.sessao.iniciar()])
    expect(falsa.estado.familiaRevogada).toBe(true)
  })

  // Sabotagem prevista: em encerrar(), trocar "if (avisarOutras)" por
  // "if (avisarOutras && Math.random() > 2)" (não posta no canal).
  it('sair numa aba faz a outra largar o token na hora', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const [a, b] = canais()
    const aba1 = novaSessao({ canal: a })
    const aba2 = novaSessao({ canal: b })
    await aba1.sessao.iniciar()
    await aba2.sessao.iniciar()
    await aba1.sessao.sair()
    await vi.waitFor(() => expect(aba2.sessao.token()).toBeNull())
    expect(aba2.sessao.estado()).toBe('anonimo')
    expect(aba2.sessao.motivo()).toBe('outra-aba')
  })

  it('o canal nunca leva o token', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const { sessao } = novaSessao({ canal: a })
    await sessao.entrar(EMAIL, SENHA)
    await sessao.sair()
    const avisos = await ateASentinela(a, recebidos)
    expect(avisos).toEqual([{ tipo: 'iniciada' }, { tipo: 'encerrada' }])
    expect(JSON.stringify(avisos)).not.toContain('acesso-')
  })
})

describe('erros de rede', () => {
  // Sem rede de verdade: o fetch rejeita com TypeError (um 502 é resposta,
  // e nunca chegaria ao catch de sair()). Sabotagem prevista: em sair(),
  // trocar "if (!(e instanceof ApiError)) throw e" por
  // "if (!(e instanceof ApiError) || e.code === 'network') throw e".
  it('logout sem rede ainda larga a sessão local', async () => {
    usarApiFalsa()
    let semRede = false
    const buscar = (entrada: string, init: RequestInit) =>
      semRede ? Promise.reject(new TypeError('Failed to fetch')) : buscarDoTeste(entrada, init)
    const { sessao } = novaSessao({ buscar })
    await sessao.entrar(EMAIL, SENHA)
    semRede = true
    await sessao.sair()
    expect(sessao.token()).toBeNull()
    expect(sessao.estado()).toBe('anonimo')
  })

  it('refresh com erro 5xx sobe como ApiError, sem encerrar a sessão', async () => {
    const falsa = usarApiFalsa()
    const { sessao } = novaSessao()
    await sessao.entrar(EMAIL, SENHA)
    falsa.estado.forcar.set('POST /auth/refresh', { status: 500, code: 'internal_error' })
    await expect(sessao.renovar(sessao.token())).rejects.toBeInstanceOf(ApiError)
    expect(sessao.estado()).toBe('autenticado')
  })
})
