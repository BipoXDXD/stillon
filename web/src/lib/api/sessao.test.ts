import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EMAIL, RECUPERACAO_OK, SENHA, TOTP_OK, type ErroForcado } from '@/test/api-falsa'
import { buscarDoTeste, novaSessao, travaFalsa, usarApiFalsa } from '@/test/ambiente'
import { ApiError, type Buscar } from './http'
import type { Canal, Estado, Travar } from './sessao'

// Dois BroadcastChannel do mesmo nome, como duas abas. O nome é novo a cada
// chamada: as sessões de um teste continuam vivas depois dele, e num nome
// compartilhado reagiriam aos avisos do teste seguinte (tentarDeNovo contra
// a API falsa dele: refreshes a mais, avisos a mais).
function canais(): [Canal, Canal] {
  const nome = `teste-sessao-${crypto.randomUUID()}`
  const a = new BroadcastChannel(nome)
  const b = new BroadcastChannel(nome)
  return [a, b]
}

// Uma trava segura de fora: quem pede espera até soltar(). Imita a outra aba
// segurando a Web Lock (o jsdom não tem navigator.locks).
function travaSegura() {
  let soltar!: () => void
  const portao = new Promise<void>((r) => (soltar = r))
  let esperando = 0
  const travar: Travar = async <T,>(_nome: string, fn: () => Promise<T>): Promise<T> => {
    esperando++
    await portao
    return fn()
  }
  return { travar, soltar: () => soltar(), esperando: () => esperando }
}

// O logout que não chega ao servidor (sem rede): o cookie de refresh continua
// valendo lá, e só a época da sessão impede um refresh de ressuscitá-la.
const logoutSemRede = (entrada: string, init: RequestInit) =>
  entrada.endsWith('/auth/logout') ? Promise.reject(new TypeError('Failed to fetch')) : buscarDoTeste(entrada, init)

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

// Segura a RESPOSTA do refresh: o servidor já decidiu, e a aba só sabe quando
// o teste soltar. Depois de soltar, os refreshes seguintes passam direto, a
// não ser que esperaDoSegundo segure também a resposta do segundo.
function refreshSegurado(esperaDoSegundo?: Promise<void>) {
  let soltar!: () => void
  const portao = new Promise<void>((r) => (soltar = r))
  let marcarVolta!: () => void
  const voltou = new Promise<void>((r) => (marcarVolta = r))
  let marcarVoltaDoSegundo!: () => void
  const voltouOSegundo = new Promise<void>((r) => (marcarVoltaDoSegundo = r))
  let refreshes = 0
  const buscar = async (entrada: string, init: RequestInit) => {
    const r = await buscarDoTeste(entrada, init)
    if (!entrada.endsWith('/auth/refresh')) return r
    refreshes++
    if (refreshes === 1) {
      marcarVolta()
      await portao
    } else if (esperaDoSegundo) {
      marcarVoltaDoSegundo()
      await esperaDoSegundo
    }
    return r
  }
  return { buscar, voltou, voltouOSegundo, soltar: () => soltar() }
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
  // que o deixa vermelho é tirar as duas ("boot ??=" → "boot =" e o
  // "if (emVoo?.epoca === minha) return" de renovar()); tirar só uma fica verde.
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

  // M5 da revisão final: uma aba ainda no boot ('verificando') ignorava o
  // "encerrada" da outra. Se o refresh do boot já tinha sido aceito pelo
  // servidor antes do logout, a aba entrava numa sessão que o dono acabou
  // de encerrar. O buscar segura a RESPOSTA do refresh (o servidor já
  // rotacionou) até o aviso chegar. Sabotagem: no ouvinte do canal, voltar a
  // "estado === 'autenticado'" só → a aba termina autenticada.
  it('"encerrada" durante o boot: o boot termina anônimo, mesmo com o refresh aceito', async () => {
    const falsa = usarApiFalsa()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const aba1 = novaSessao({ canal: a })
    await aba1.sessao.entrar(EMAIL, SENHA)
    let soltar!: () => void
    const portao = new Promise<void>((r) => (soltar = r))
    let refreshVoltou = false
    const buscar = async (entrada: string, init: RequestInit) => {
      const r = await buscarDoTeste(entrada, init)
      if (entrada.endsWith('/auth/refresh')) {
        refreshVoltou = true
        await portao
      }
      return r
    }
    const aba2 = novaSessao({ canal: b, buscar })
    const boot = aba2.sessao.iniciar()
    await vi.waitFor(() => expect(refreshVoltou).toBe(true))
    await aba1.sessao.sair()
    await ateASentinela(a, recebidos)
    soltar()
    await boot
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(aba2.sessao.token()).toBeNull()
    expect(aba2.sessao.estado()).toBe('anonimo')
    expect(aba2.sessao.motivo()).toBe('outra-aba')
  })

  // O mesmo, com a rede caindo no refresh do boot: a sessão acabou na outra
  // aba, e isto é "anônimo" (vai ao login com o motivo), não "offline".
  // Sabotagem: em iniciar(), trocar "if (minha === epoca) estado = novo"
  // por "estado = novo" → offline.
  it('"encerrada" durante o boot com o refresh sem rede: anônimo, e não offline', async () => {
    usarApiFalsa()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const aba1 = novaSessao({ canal: a })
    await aba1.sessao.entrar(EMAIL, SENHA)
    let soltar!: () => void
    const portao = new Promise<void>((r) => (soltar = r))
    let refreshSaiu = false
    const buscar = async (entrada: string, init: RequestInit) => {
      if (!entrada.endsWith('/auth/refresh')) return buscarDoTeste(entrada, init)
      refreshSaiu = true
      await portao
      throw new TypeError('Failed to fetch')
    }
    const aba2 = novaSessao({ canal: b, buscar })
    const boot = aba2.sessao.iniciar()
    await vi.waitFor(() => expect(refreshSaiu).toBe(true))
    await aba1.sessao.sair()
    await ateASentinela(a, recebidos)
    soltar()
    await boot
    expect(aba2.sessao.estado()).toBe('anonimo')
    expect(aba2.sessao.motivo()).toBe('outra-aba')
  })

  // Sabotagem prevista: tirar a linha do "iniciada" no ouvinte do canal → a
  // aba fica anônima, parada no login.
  it('"iniciada" de outra aba: a aba anônima entra junto, pelo cookie', async () => {
    const falsa = usarApiFalsa()
    const [a, b] = canais()
    const aba1 = novaSessao({ canal: a })
    const aba2 = novaSessao({ canal: b })
    await aba2.sessao.iniciar()
    expect(aba2.sessao.estado()).toBe('anonimo')
    await aba1.sessao.entrar(EMAIL, SENHA)
    await vi.waitFor(() => expect(aba2.sessao.estado()).toBe('autenticado'))
    expect(aba2.sessao.token()).not.toBeNull()
    // Um do boot, sem cookie, e um do aviso.
    expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
    expect(falsa.estado.familiaRevogada).toBe(false)
  })

  // Só a aba anônima reage. Uma aba autenticada que passasse por "verificando"
  // desmontaria o Layout e perderia o formulário pela metade. Sabotagem
  // prevista: tirar o "&& estado === 'anonimo'" do "iniciada".
  it('"iniciada" de outra aba: a aba autenticada não renova nem passa por verificando', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const aba1 = novaSessao({ canal: a })
    const aba2 = novaSessao({ canal: b })
    await aba2.sessao.iniciar()
    const tokenAntes = aba2.sessao.token()
    const estados: Estado[] = []
    aba2.sessao.assinar(() => estados.push(aba2.sessao.estado()))
    await aba1.sessao.entrar(EMAIL, SENHA)
    expect(await ateASentinela(a, recebidos)).toEqual([{ tipo: 'iniciada' }])
    expect(estados).toEqual([])
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(aba2.sessao.token()).toBe(tokenAntes)
    expect(aba2.sessao.estado()).toBe('autenticado')
  })

  // A outra fronteira da condição: quem sai do "offline" é o "Tentar de novo".
  // Sabotagem prevista: trocar por "estado !== 'autenticado'" → sai um refresh.
  it('"iniciada" de outra aba: a aba offline continua offline, sem renovar', async () => {
    const falsa = usarApiFalsa()
    falsa.estado.forcar.set('POST /auth/refresh', { status: 503, code: 'unavailable' })
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    const aba1 = novaSessao({ canal: a })
    const aba2 = novaSessao({ canal: b })
    await aba2.sessao.iniciar()
    expect(aba2.sessao.estado()).toBe('offline')
    falsa.estado.forcar.delete('POST /auth/refresh')
    await aba1.sessao.entrar(EMAIL, SENHA)
    expect(await ateASentinela(a, recebidos)).toEqual([{ tipo: 'iniciada' }])
    expect(aba2.sessao.estado()).toBe('offline')
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
  })

  // A terceira fronteira da condição: o boot em voo já vai decidir pelo cookie,
  // e um segundo refresh seria reúso. O buscar segura a RESPOSTA do refresh até
  // o aviso chegar. Sabotagem prevista: trocar por
  // "(estado === 'anonimo' || estado === 'verificando')" → sai outro refresh.
  it('"iniciada" de outra aba: a aba ainda no boot termina o boot, sem renovar de novo', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    const [a, b] = canais()
    const recebidos: unknown[] = []
    b.addEventListener('message', (e) => recebidos.push(e.data))
    let soltar!: () => void
    const portao = new Promise<void>((r) => (soltar = r))
    let refreshVoltou = false
    const buscar = async (entrada: string, init: RequestInit) => {
      const r = await buscarDoTeste(entrada, init)
      if (entrada.endsWith('/auth/refresh')) {
        refreshVoltou = true
        await portao
      }
      return r
    }
    const aba1 = novaSessao({ canal: a })
    const aba2 = novaSessao({ canal: b, buscar })
    const estados: Estado[] = []
    aba2.sessao.assinar(() => estados.push(aba2.sessao.estado()))
    const boot = aba2.sessao.iniciar()
    await vi.waitFor(() => expect(refreshVoltou).toBe(true))
    expect(aba2.sessao.estado()).toBe('verificando')
    await aba1.sessao.entrar(EMAIL, SENHA)
    expect(await ateASentinela(a, recebidos)).toEqual([{ tipo: 'iniciada' }])
    soltar()
    await boot
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.estado.familiaRevogada).toBe(false)
    expect(estados).toEqual(['autenticado'])
    expect(aba2.sessao.token()).not.toBeNull()
  })

  // O refresh do boot saiu antes de o cookie existir, e a outra aba entrou
  // enquanto a resposta voltava: o 401 dele já não diz nada sobre o cookie.
  describe('"iniciada" com a resposta do refresh do boot a caminho', () => {
    async function bootComLoginNoMeio({
      primeiroRefresh,
      refresh = refreshSegurado(),
    }: { primeiroRefresh?: ErroForcado; refresh?: ReturnType<typeof refreshSegurado> } = {}) {
      const falsa = usarApiFalsa()
      if (primeiroRefresh) falsa.estado.forcar.set('POST /auth/refresh', primeiroRefresh)
      const [a, b] = canais()
      const recebidos: unknown[] = []
      b.addEventListener('message', (e) => recebidos.push(e.data))
      const aba1 = novaSessao({ canal: a, buscar: logoutSemRede })
      const aba2 = novaSessao({ canal: b, buscar: refresh.buscar, travar: travaFalsa() })
      const estados: Estado[] = []
      aba2.sessao.assinar(() => estados.push(aba2.sessao.estado()))
      const boot = aba2.sessao.iniciar()
      await refresh.voltou
      expect(aba2.sessao.estado()).toBe('verificando')
      await aba1.sessao.entrar(EMAIL, SENHA)
      expect(await ateASentinela(a, recebidos)).toEqual([{ tipo: 'iniciada' }])
      // A sentinela velha no fim da lista faria a próxima espera voltar na hora.
      recebidos.length = 0
      return { falsa, a, recebidos, aba1, aba2, estados, boot, soltar: refresh.soltar }
    }

    // Sem passar por "anonimo": a rota protegida mandaria a aba ao /login.
    // Sabotagem prevista: em iniciar(), tirar o segundo refresh → um refresh
    // só, e a aba termina anônima.
    it('a aba entra pelo cookie, sem passar por anônimo', async () => {
      const { falsa, aba2, estados, boot, soltar } = await bootComLoginNoMeio()
      soltar()
      await boot
      expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
      expect(falsa.estado.familiaRevogada).toBe(false)
      expect(estados).toEqual(['autenticado'])
      expect(aba2.sessao.token()).not.toBeNull()
    })

    // Só o 401 fica em dúvida. Um boot que termina offline segue a regra da aba
    // offline: quem sai dali é o "Tentar de novo". Sabotagem prevista: tirar o
    // "novo === 'anonimo'" da condição do segundo refresh → sai outro refresh.
    it('boot que termina offline continua offline, sem renovar de novo', async () => {
      const { falsa, aba2, estados, boot, soltar } = await bootComLoginNoMeio({
        primeiroRefresh: { status: 503, code: 'unavailable' },
      })
      falsa.estado.forcar.delete('POST /auth/refresh')
      soltar()
      await boot
      expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
      expect(estados).toEqual(['offline'])
      expect(aba2.sessao.token()).toBeNull()
    })

    // Sabotagem prevista: fazer o segundo refresh chamar renovar(null) direto,
    // fora do estadoPeloCookie → o 503 rejeita o boot, em vez de terminar offline.
    it.each<{ erro: ErroForcado; fim: Estado }>([
      { erro: { status: 401, code: 'invalid_session' }, fim: 'anonimo' },
      { erro: { status: 503, code: 'unavailable' }, fim: 'offline' },
    ])('o segundo refresh decide: $erro.status termina $fim', async ({ erro, fim }) => {
      const { falsa, aba2, estados, boot, soltar } = await bootComLoginNoMeio()
      falsa.estado.forcar.set('POST /auth/refresh', erro)
      soltar()
      await boot
      expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
      expect(estados).toEqual([fim])
      expect(aba2.sessao.token()).toBeNull()
    })

    // O logout não chegou ao servidor, e o cookie continua valendo: um segundo
    // refresh ressuscitaria a sessão que o dono acabou de encerrar. Sabotagem
    // prevista: tirar o "minha === epoca" da condição do segundo refresh.
    it('"encerrada" logo depois: a época nova cancela o segundo refresh', async () => {
      const { falsa, a, recebidos, aba1, aba2, boot, soltar } = await bootComLoginNoMeio()
      await aba1.sessao.sair()
      await ateASentinela(a, recebidos)
      soltar()
      await boot
      expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
      expect(aba2.sessao.token()).toBeNull()
      expect(aba2.sessao.estado()).toBe('anonimo')
      expect(aba2.sessao.motivo()).toBe('outra-aba')
    })

    // O logout e o login novo cruzam com o refresh em voo: o "iniciada" move a
    // aba, já anônima, para um boot novo, e a resposta do refresh antigo é de
    // outra época. Sabotagem prevista: em renovar(), voltar a reaproveitar o
    // emVoo de qualquer época → um refresh só, e a aba termina anônima.
    it('"encerrada" e "iniciada": o boot novo faz o próprio refresh e entra', async () => {
      const { falsa, a, recebidos, aba1, aba2, boot, soltar } = await bootComLoginNoMeio()
      await aba1.sessao.sair()
      await aba1.sessao.entrar(EMAIL, SENHA)
      await ateASentinela(a, recebidos)
      expect(aba2.sessao.estado()).toBe('verificando')
      soltar()
      await boot
      await aba2.sessao.iniciar()
      expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
      expect(falsa.estado.familiaRevogada).toBe(false)
      expect(aba2.sessao.estado()).toBe('autenticado')
      expect(aba2.sessao.token()).not.toBeNull()
    })

    // Sabotagem prevista: no .finally de renovar(), limpar o emVoo sem conferir
    // a promessa → o fim do refresh antigo apaga o novo, e sai um terceiro.
    it('o fim do refresh da época antiga não apaga o refresh em voo da nova', async () => {
      let abrirOSegundo!: () => void
      const esperaDoSegundo = new Promise<void>((r) => (abrirOSegundo = r))
      const refresh = refreshSegurado(esperaDoSegundo)
      const { falsa, a, recebidos, aba1, aba2, boot, soltar } = await bootComLoginNoMeio({ refresh })
      await aba1.sessao.sair()
      await aba1.sessao.entrar(EMAIL, SENHA)
      await ateASentinela(a, recebidos)
      soltar()
      await boot
      await refresh.voltouOSegundo
      const carona = aba2.sessao.renovar(null)
      abrirOSegundo()
      await aba2.sessao.iniciar()
      await carona
      expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
      expect(falsa.estado.familiaRevogada).toBe(false)
      expect(aba2.sessao.estado()).toBe('autenticado')
    })
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

  // A9 do Plano 10: um logout que o servidor não conseguiu fazer responde
  // 500 e não apaga o cookie. A aba não larga a sessão: o erro sobe, e o
  // Sair pode ser tentado de novo. Sem rede continua como acima (a aba
  // larga). Sabotagem: tirar o "if (r && !r.ok) throw" de sair() → anônimo.
  it('logout que o servidor recusa sobe o erro e não larga a sessão', async () => {
    const falsa = usarApiFalsa()
    const { sessao } = novaSessao()
    await sessao.entrar(EMAIL, SENHA)
    falsa.estado.forcar.set('POST /auth/logout', { status: 500, code: 'internal_error' })
    await expect(sessao.sair()).rejects.toMatchObject({ status: 500, code: 'internal_error' })
    expect(sessao.token()).not.toBeNull()
    expect(sessao.estado()).toBe('autenticado')
    falsa.estado.forcar.delete('POST /auth/logout')
    await sessao.sair()
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('saiu')
  })

  // M5 da revisão final: um logout que nunca responde (rede presa) travava
  // o "Sair" para sempre. Com o prazo, a aba larga a sessão do mesmo jeito.
  // O buscar imita o fetch: só rejeita quando o sinal aborta. Sabotagem:
  // tirar o "sinal: AbortSignal.timeout(prazoMs)" do logout → "travou".
  it('logout que nunca responde termina no prazo, e a sessão local acaba', async () => {
    usarApiFalsa()
    const logoutParado = (entrada: string, init: RequestInit) =>
      entrada.endsWith('/auth/logout')
        ? new Promise<Response>((_, rejeitar) => {
            init.signal?.addEventListener('abort', () => rejeitar(init.signal?.reason))
          })
        : buscarDoTeste(entrada, init)
    const { sessao } = novaSessao({ buscar: logoutParado, prazoMs: 50 })
    await sessao.entrar(EMAIL, SENHA)
    const r = await Promise.race([
      sessao.sair().then(() => 'saiu'),
      new Promise((ok) => setTimeout(() => ok('travou'), 1000)),
    ])
    expect(r).toBe('saiu')
    expect(sessao.token()).toBeNull()
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('saiu')
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

describe('o prazo do refresh', () => {
  const PRAZO_CURTO_MS = 50
  const PRAZO_PADRAO_MS = 10_000

  // Imita o fetch: só rejeita quando o sinal aborta. Sem sinal, nunca termina.
  const refreshParado = (_entrada: string, init: RequestInit) =>
    new Promise<Response>((_, rejeitar) => {
      init.signal?.addEventListener('abort', () => rejeitar(init.signal?.reason))
    })

  // O AbortSignal.timeout é do ambiente de execução e tem relógio próprio,
  // fora do alcance do relógio falso. Só ele é trocado: o mesmo erro, no
  // setTimeout falso. O prazo de verdade passa pelo fetch no teste do logout.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    vi.spyOn(AbortSignal, 'timeout').mockImplementation((ms) => {
      const controle = new AbortController()
      setTimeout(() => controle.abort(new DOMException('The operation timed out.', 'TimeoutError')), ms)
      return controle.signal
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  // Sabotagem prevista: tirar o "sinal: AbortSignal.timeout(prazoMs)" do
  // refresh → a sessão fica em "verificando", e o splash não sai da tela. O
  // caso padrão também pega trocar o 10_000.
  it.each([
    { prazoMs: PRAZO_CURTO_MS, prazo: PRAZO_CURTO_MS },
    { prazoMs: undefined, prazo: PRAZO_PADRAO_MS },
  ])('refresh que nunca responde: o boot termina offline em $prazo ms, nem antes', async ({ prazoMs, prazo }) => {
    const { sessao } = novaSessao({ buscar: refreshParado, travar: travaFalsa(), prazoMs })
    void sessao.iniciar()
    await vi.advanceTimersByTimeAsync(prazo - 1)
    expect(sessao.estado()).toBe('verificando')
    await vi.advanceTimersByTimeAsync(1)
    expect(sessao.estado()).toBe('offline')
    expect(sessao.token()).toBeNull()
  })

  // A trava é de todas as abas: um refresh parado não pode segurá-la. A outra
  // aba responde aqui mesmo, porque a API falsa também espera no setTimeout, e
  // ele está parado. Sabotagem: a mesma, e a outra aba nunca entra na trava.
  it('o prazo vencido solta a trava, e a outra aba entra nela', async () => {
    const travar = travaFalsa()
    const buscarDaOutraAba = vi.fn<Buscar>(async () => new Response(null, { status: 401 }))
    const aba1 = novaSessao({ buscar: refreshParado, travar, prazoMs: PRAZO_CURTO_MS })
    const aba2 = novaSessao({ buscar: buscarDaOutraAba, travar, prazoMs: PRAZO_CURTO_MS })
    void aba1.sessao.iniciar()
    void aba2.sessao.iniciar()
    await vi.advanceTimersByTimeAsync(PRAZO_CURTO_MS - 1)
    expect(buscarDaOutraAba).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(buscarDaOutraAba).toHaveBeenCalledTimes(1)
    expect(aba2.sessao.estado()).toBe('anonimo')
  })
})

describe('a época da sessão', () => {
  // Caminho (a): o refresh espera a trava (outra aba a segura), e a sessão
  // acaba nesse meio-tempo com um logout sem rede. Quando a trava solta, o
  // refresh ainda é aceito pelo servidor (o cookie ficou), mas a resposta é
  // de outra época e não mexe no token. Sabotagem prevista: capturar a época
  // dentro de renovarAgora(), depois da trava ("minha = epoca" na primeira
  // linha de renovarAgora) → o pedido resolve e o token volta.
  it('refresh na fila da trava quando a sessão acaba não ressuscita o token', async () => {
    const falsa = usarApiFalsa()
    const trava = travaSegura()
    const { sessao, api } = novaSessao({ travar: trava.travar, buscar: logoutSemRede })
    await sessao.entrar(EMAIL, SENHA)
    falsa.expirarAcessos()
    const pedido = api.me().then(
      () => 'resolveu',
      (e: unknown) => e,
    )
    await vi.waitFor(() => expect(trava.esperando()).toBe(1))
    await sessao.sair()
    expect(sessao.estado()).toBe('anonimo')
    trava.soltar()
    expect(await pedido).toMatchObject({ code: 'unauthorized' })
    // O refresh saiu e o servidor o aceitou: a corrida é real, e só a época
    // segurou o token.
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(sessao.token()).toBeNull()
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('saiu')
  })

  // Caminho (b), sem trava: a requisição saiu com o token T, a sessão acabou
  // (logout sem rede), e o 401 de T chega depois. T é de uma sessão que já
  // acabou: renovar(T) não renova. Sabotagem prevista: em renovar(), trocar
  // "if (usado !== null && token === null)" por
  // "if (usado !== null && token === null && Math.random() > 2)" → um
  // refresh sai, e o pedido resolve com o token novo.
  it('401 de uma requisição que saiu antes de a sessão acabar não renova', async () => {
    const falsa = usarApiFalsa()
    let soltarMe!: () => void
    const portaoMe = new Promise<void>((r) => (soltarMe = r))
    let meSaiu = false
    const buscar = (entrada: string, init: RequestInit) => {
      if (entrada.endsWith('/me')) {
        meSaiu = true
        return portaoMe.then(() => logoutSemRede(entrada, init))
      }
      return logoutSemRede(entrada, init)
    }
    const { sessao, api } = novaSessao({ buscar, travar: travaFalsa() })
    await sessao.entrar(EMAIL, SENHA)
    falsa.expirarAcessos()
    const pedido = api.me().then(
      () => 'resolveu',
      (e: unknown) => e,
    )
    await vi.waitFor(() => expect(meSaiu).toBe(true))
    await sessao.sair()
    soltarMe()
    expect(await pedido).toMatchObject({ code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
    expect(sessao.token()).toBeNull()
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('saiu')
  })
})
