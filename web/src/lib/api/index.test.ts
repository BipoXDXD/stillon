import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EMAIL, SENHA } from '@/test/api-falsa'
import { usarApiFalsa } from '@/test/ambiente'

const canais: BroadcastChannel[] = []
let locksOriginal: PropertyDescriptor | undefined

beforeEach(() => {
  vi.resetModules()
  locksOriginal = Object.getOwnPropertyDescriptor(navigator, 'locks')
  const buscar = globalThis.fetch
  vi.stubGlobal('fetch', (entrada: RequestInfo | URL, init?: RequestInit) =>
    buscar(typeof entrada === 'string' ? new URL(entrada, location.href) : entrada, init))
  const CanalReal = BroadcastChannel
  vi.stubGlobal('BroadcastChannel', class extends CanalReal {
    constructor(nome: string) {
      super(nome)
      canais.push(this)
    }
  })
})

afterEach(() => {
  canais.forEach((canal) => canal.close())
  canais.length = 0
  vi.unstubAllGlobals()
  if (locksOriginal) Object.defineProperty(navigator, 'locks', locksOriginal)
  else Reflect.deleteProperty(navigator, 'locks')
})

describe('instâncias usadas pelo painel', () => {
  // Remover a trava/canal em index.ts deixa sair HTTP antes da liberação ou
  // mantém a API com o token de uma sessão encerrada em outra aba.
  it('espera a trava do navegador e larga a mesma sessão usada pela API ao receber o logout de outra aba', async () => {
    const falsa = usarApiFalsa()
    falsa.logado()
    let liberar!: () => void
    const portao = new Promise<void>((resolve) => { liberar = resolve })
    const travas: string[] = []
    Object.defineProperty(navigator, 'locks', {
      configurable: true,
      value: {
        async request<T>(nome: string, fn: () => Promise<T>): Promise<T> {
          travas.push(nome)
          await portao
          return fn()
        },
      },
    })
    const outraAba = new BroadcastChannel('stillon:sessao')
    const { sessao, api } = await import('./index')
    const boot = sessao.iniciar()

    await vi.waitFor(() => expect(travas).toEqual(['stillon:refresh']))
    expect(falsa.estado.chamadas.get('POST /auth/refresh') ?? 0).toBe(0)
    liberar()
    await boot
    await expect(api.me()).resolves.toMatchObject({ email: EMAIL })

    outraAba.postMessage({ tipo: 'encerrada' })
    await vi.waitFor(() => expect(sessao.estado()).toBe('anonimo'))
    expect(sessao.motivo()).toBe('outra-aba')
    await expect(api.me()).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(falsa.estado.chamadas.get('POST /auth/refresh')).toBe(1)
  })

  // Um canal omitido ou publicar access_token em vez de eventos rompe o
  // contrato com as outras abas, mesmo que a sessão local continue a entrar.
  it('comunica login e logout por eventos reais entre abas sem transmitir o access token', async () => {
    usarApiFalsa()
    const outraAba = new BroadcastChannel('stillon:sessao')
    const recebidos: unknown[] = []
    outraAba.addEventListener('message', (e) => recebidos.push(e.data))
    const { sessao, api } = await import('./index')

    await sessao.iniciar()
    await sessao.entrar(EMAIL, SENHA)
    await expect(api.me()).resolves.toMatchObject({ email: EMAIL })
    await vi.waitFor(() => expect(recebidos).toEqual([{ tipo: 'iniciada' }]))
    await sessao.sair()
    await vi.waitFor(() => expect(recebidos).toEqual([{ tipo: 'iniciada' }, { tipo: 'encerrada' }]))
    // A sentinela vem do mesmo emissor real: quando ela chega, todos os
    // eventos anteriores também chegaram, inclusive eventual vazamento.
    canais.at(-1)!.postMessage({ tipo: 'sentinela-do-teste' })
    await vi.waitFor(() => expect(recebidos.at(-1)).toEqual({ tipo: 'sentinela-do-teste' }))
    expect(recebidos.slice(0, -1)).toEqual([{ tipo: 'iniciada' }, { tipo: 'encerrada' }])
    expect(sessao.token()).toBeNull()
    await expect(api.me()).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
  })
})
