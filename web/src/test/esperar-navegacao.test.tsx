import { setImmediate as concluirTurno } from 'node:timers/promises'
import { act, screen } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { rotas } from '@/app/rotas'
import { esperarNavegacao } from './esperar-navegacao'
import { renderizarPainel } from './renderizar'

const captura = vi.hoisted(() => ({
  respostaPresa: undefined as Promise<void> | undefined,
  respondeu: undefined as (() => void) | undefined,
}))
// O POST continua atravessando a sessão, o fetch e o handler reais da fixture.
// Somente a entrega da resposta recebida fica atrás de um portão público.
vi.mock('@/test/ambiente', async (original) => {
  const modulo = await original<typeof import('./ambiente')>()
  return {
    ...modulo,
    novaSessao: (...args: Parameters<typeof modulo.novaSessao>) => {
      const opcoes = args[0]
      const buscar = opcoes?.buscar ?? modulo.buscarDoTeste
      return modulo.novaSessao({
        ...opcoes,
        buscar: async (entrada, init) => {
          const resposta = await buscar(entrada, init)
          if (entrada.endsWith('/targets') && init.method === 'POST') {
            captura.respondeu?.()
            await captura.respostaPresa
          }
          return resposta
        },
      })
    },
  }
})
beforeEach(() => {
  captura.respostaPresa = undefined
  captura.respondeu = undefined
})

function portao() {
  let abrir!: () => void
  const aberto = new Promise<void>((resolve) => { abrir = resolve })
  return { aberto, abrir }
}

function acompanhar(promessa: Promise<void>) {
  let estado: 'pendente' | 'concluida' | 'rejeitada' = 'pendente'
  const resultado = promessa.then(
    () => { estado = 'concluida'; return { erro: undefined } },
    (erro: unknown) => { estado = 'rejeitada'; return { erro } },
  )
  return { estado: () => estado, resultado }
}

function observarAbort(signal: AbortSignal) {
  const adicionar = signal.addEventListener.bind(signal)
  const remover = signal.removeEventListener.bind(signal)
  const ouvintes = new Map<EventListenerOrEventListenerObject, EventListener>()
  let entregas = 0
  let sentinela = 0
  const contarSentinela = () => { sentinela++ }
  adicionar('abort', contarSentinela)
  const add = vi.spyOn(signal, 'addEventListener').mockImplementation((tipo, ouvinte, opcoes) => {
    if (tipo !== 'abort' || !ouvinte) return adicionar(tipo, ouvinte, opcoes)
    const delegado: EventListener = (evento) => {
      entregas++
      if (typeof ouvinte === 'function') ouvinte.call(signal, evento)
      else ouvinte.handleEvent(evento)
    }
    ouvintes.set(ouvinte, delegado)
    adicionar(tipo, delegado, opcoes)
  })
  const remove = vi.spyOn(signal, 'removeEventListener').mockImplementation((tipo, ouvinte, opcoes) => {
    remover(tipo, ouvinte ? (ouvintes.get(ouvinte) ?? ouvinte) : ouvinte, opcoes)
  })
  return {
    entregas: () => entregas,
    sentinela: () => sentinela,
    restaurar() {
      for (const delegado of ouvintes.values()) remover('abort', delegado)
      remover('abort', contarSentinela)
      add.mockRestore()
      remove.mockRestore()
    },
  }
}

it('espera o destino idle após o POST e limpa observadores com eventos reais', async ({ signal }) => {
  const rota = rotas[1]?.children?.[0]?.children?.find((r) => r.path === 'targets/:id')
  if (!rota || typeof rota.lazy !== 'function') throw new Error('lazy real do detalhe ausente')
  const original = rota.lazy
  const post = portao()
  const liberarPost = portao()
  const lazy = portao()
  const liberarLazy = portao()
  captura.respostaPresa = liberarPost.aberto
  captura.respondeu = post.abrir
  rota.lazy = async (...args) => {
    lazy.abrir()
    await liberarLazy.aberto
    return original(...args)
  }
  const a = new AbortController()
  const b = new AbortController()
  const cancelarA = () => a.abort(signal.reason)
  signal.addEventListener('abort', cancelarA)
  const abortA = observarAbort(a.signal)
  const abortB = observarAbort(b.signal)
  let r: Awaited<ReturnType<typeof renderizarPainel>> | undefined
  let pararSentinela: (() => void) | undefined
  let restaurarSubscribe: (() => void) | undefined
  try {
    r = await renderizarPainel('/targets/new')
    const router = r.router
    const subscribe = router.subscribe.bind(router)
    let entregas = 0
    let sentinela = 0
    pararSentinela = subscribe(() => { sentinela++ })
    const observacao = vi.spyOn(router, 'subscribe').mockImplementation((ouvinte) => subscribe((...args) => {
      entregas++
      ouvinte(...args)
    }))
    restaurarSubscribe = () => observacao.mockRestore()
    const esperaA = acompanhar(esperarNavegacao(router, '/targets/alvo-1', a.signal))
    await r.usuario.type(screen.getByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    await act(async () => {
      await r!.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
      await post.aberto
      await concluirTurno()
      expect(r!.falsa.chamadas('POST /targets')).toBe(1)
      expect(router.state.location.pathname).toBe('/targets/new')
      expect(router.state.navigation.state).toBe('idle')
      expect(esperaA.estado()).toBe('pendente')
      liberarPost.abrir()
      await lazy.aberto
      await concluirTurno()
      expect(router.state.location.pathname).toBe('/targets/new')
      expect(router.state.navigation.state).toBe('loading')
      expect(esperaA.estado()).toBe('pendente')
      // Mesmo destino atual: durante loading não há prontidão. Nenhuma
      // navegação extra é disparada para criar esse estado.
      const esperaB = acompanhar(esperarNavegacao(router, '/targets/new', b.signal))
      await concluirTurno()
      expect(esperaB.estado()).toBe('pendente')
      const motivo = new Error('destino atual deixou de ser o destino desejado')
      b.abort(motivo)
      await concluirTurno()
      expect(esperaB.estado()).toBe('rejeitada')
      expect((await esperaB.resultado).erro).toBe(motivo)
      liberarLazy.abrir()
      expect((await esperaA.resultado).erro).toBeUndefined()
      expect(router.state.location.pathname).toBe('/targets/alvo-1')
      expect(router.state.navigation.state).toBe('idle')
    })
    expect(await screen.findByRole('heading', { name: 'Site' })).toBeInTheDocument()
    expect(r.falsa.corpos('POST /targets')).toEqual([{
      name: 'Site', url: 'https://exemplo.com', method: 'GET', interval_sec: 60,
      timeout_ms: 10000, expected_status: 200, enabled: true,
    }])
    const entregasAntes = entregas
    const sentinelaAntes = sentinela
    await act(() => router.revalidate())
    expect(sentinela).toBeGreaterThan(sentinelaAntes)
    expect(entregas).toBe(entregasAntes)
    a.abort(new Error('encerrado depois do sucesso'))
    expect(abortA.sentinela()).toBe(1)
    expect(abortA.entregas()).toBe(0)
    expect(abortB.sentinela()).toBe(1)
    expect(abortB.entregas()).toBe(1)
  } finally {
    await act(async () => {
      liberarPost.abrir()
      liberarLazy.abrir()
      await concluirTurno()
      const router = r?.router
      if (router && router.state.navigation.state !== 'idle' && !signal.aborted) {
        await new Promise<void>((resolve) => {
          const concluir = () => {
            parar()
            signal.removeEventListener('abort', concluir)
            resolve()
          }
          const parar = router.subscribe((estado) => {
            if (estado.navigation.state === 'idle') concluir()
          })
          signal.addEventListener('abort', concluir)
          if (signal.aborted || router.state.navigation.state === 'idle') concluir()
        })
      }
    })
    a.abort()
    b.abort()
    signal.removeEventListener('abort', cancelarA)
    pararSentinela?.()
    restaurarSubscribe?.()
    abortA.restaurar()
    abortB.restaurar()
    r?.router.dispose()
    rota.lazy = original
  }
})

it('destino já idle resolve e sinal já abortado rejeita com a reason original', async ({ signal }) => {
  const r = await renderizarPainel('/targets/new')
  try {
    expect(r.router.state.navigation.state).toBe('idle')
    await expect(esperarNavegacao(r.router, '/targets/new', signal)).resolves.toBeUndefined()
    const controle = new AbortController()
    const motivo = new Error('cancelado antes da espera')
    controle.abort(motivo)
    await expect(esperarNavegacao(r.router, '/targets/new', controle.signal)).rejects.toBe(motivo)
  } finally {
    r.router.dispose()
  }
})


it.each(['antes', 'depois'] as const)('abort %s do registro preserva a reason real', async (momento) => {
  const r = await renderizarPainel('/targets/new')
  const controle = new AbortController()
  const signal = controle.signal
  const motivo = new Error('cancelado durante a instalação')
  const adicionar = signal.addEventListener.bind(signal)
  // Intercala cancelamento real na fronteira pública de registro; não
  // chama o callback nem fabrica o estado do AbortSignal/router.
  const observacao = vi.spyOn(signal, 'addEventListener').mockImplementation((tipo, ouvinte, opcoes) => {
    if (momento === 'antes') controle.abort(motivo)
    adicionar(tipo, ouvinte, opcoes)
    if (momento === 'depois') controle.abort(motivo)
  })
  try {
    const espera = acompanhar(esperarNavegacao(r.router, '/targets/alvo-1', signal))
    await concluirTurno()
    expect(espera.estado()).toBe('rejeitada')
    expect((await espera.resultado).erro).toBe(motivo)
    expect(signal.aborted).toBe(true)
    await act(() => r.router.revalidate())
  } finally {
    observacao.mockRestore()
    r.router.dispose()
  }
})
