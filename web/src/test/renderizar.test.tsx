import { setImmediate as concluirTurno } from 'node:timers/promises'
import { act, screen } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { rotas } from '@/app/rotas'
import type { Sessao } from '@/lib/api/sessao'
import type { ApiFalsa } from './api-falsa'
import { alvo } from './fixtures'
import { renderizarPainel } from './renderizar'

// Captura somente fronteiras reais: a sessão/API continuam sendo as originais.
const captura = vi.hoisted(() => ({
  sessao: undefined as Sessao | undefined,
  router: undefined as ReturnType<typeof import('react-router').createMemoryRouter> | undefined,
  prepararApi: undefined as ((falsa: ApiFalsa) => void) | undefined,
  refreshPreso: undefined as Promise<void> | undefined,
  observarRefresh: undefined as (() => void) | undefined,
}))
vi.mock('@/test/ambiente', async (original) => {
  const modulo = await original<typeof import('./ambiente')>()
  return {
    ...modulo,
    novaSessao: (...args: Parameters<typeof modulo.novaSessao>) => {
      const opcoes = args[0]
      const preso = captura.refreshPreso
      const observar = captura.observarRefresh
      const buscar = opcoes?.buscar ?? modulo.buscarDoTeste
      const criada = modulo.novaSessao(preso ? {
        ...opcoes,
        buscar: async (entrada, init) => {
          if (entrada.endsWith('/auth/refresh')) {
            observar?.()
            await preso
          }
          return buscar(entrada, init)
        },
      } : opcoes)
      captura.sessao = criada.sessao
      return criada
    },
    usarApiFalsa: () => {
      const falsa = modulo.usarApiFalsa()
      captura.prepararApi?.(falsa)
      return falsa
    },
  }
})

vi.mock('react-router', async (original) => {
  const modulo = await original<typeof import('react-router')>()
  return {
    ...modulo,
    createMemoryRouter: (...args: Parameters<typeof modulo.createMemoryRouter>) => {
      const router = modulo.createMemoryRouter(...args)
      captura.router = router
      return router
    },
  }
})

beforeEach(() => {
  captura.sessao = undefined
  captura.router = undefined
  captura.prepararApi = undefined
  captura.refreshPreso = undefined
  captura.observarRefresh = undefined
})

function portao() {
  let abrir!: () => void
  const aberto = new Promise<void>((resolve) => { abrir = resolve })
  return { aberto, abrir }
}

function rotaDoDetalhe() {
  const rota = rotas[1]?.children?.[0]?.children?.find((r) => r.path === 'targets/:id')
  if (!rota || typeof rota.lazy !== 'function') throw new Error('rota lazy real do detalhe ausente')
  return rota
}

it('a fixture não conclui antes da rota lazy real, mesmo com a sessão pronta', async () => {
  const rota = rotaDoDetalhe()
  const original = rota.lazy
  if (typeof original !== 'function') throw new Error('lazy real ausente')
  const entrou = portao()
  const liberar = portao()
  const importado = portao()
  captura.prepararApi = (falsa) => { falsa.estado.alvos = [alvo()] }
  rota.lazy = async (...args) => {
    entrou.abrir()
    await liberar.aberto
    try {
      return await original(...args)
    } finally {
      importado.abrir()
    }
  }
  let concluida = false
  let concluiuAntesDaRota: boolean | undefined
  let estadoAntesDaRota: string | undefined
  let notificouSemProntidao = false
  let pararObservacao: (() => void) | undefined
  let montagem: ReturnType<typeof renderizarPainel> | undefined
  let painel: Awaited<ReturnType<typeof renderizarPainel>> | undefined
  try {
    // Uma única interação externa mantém os acts internos aninhados e
    // deixa as assertivas de DOM para depois do flush, sem sobreposição.
    await act(async () => {
      montagem = renderizarPainel('/targets/alvo-1').then((r) => { concluida = true; return r })
      await entrou.aberto
      const sessao = captura.sessao
      if (!sessao) throw new Error('sessão real não criada')
      await sessao.iniciar()
      const router = captura.router
      if (!router) throw new Error('router real não criado')
      pararObservacao = router.subscribe((estado) => {
        if (!estado.initialized) notificouSemProntidao = true
      })
      // Uma notificação pública de revalidação não equivale a montagem pronta.
      const revalidacao = router.revalidate()
      // Barreira de event loop, sem duração em ms: processa as continuações
      // do boot/act antes de observar a promessa, com o import ainda preso.
      await concluirTurno()
      estadoAntesDaRota = sessao.estado()
      concluiuAntesDaRota = concluida
      liberar.abrir()
      painel = await montagem
      await importado.aberto
      await revalidacao
    })
    expect(notificouSemProntidao).toBe(true)
    expect(estadoAntesDaRota).toBe('autenticado')
    expect(concluiuAntesDaRota).toBe(false)
    expect(painel?.router.state.initialized).toBe(true)
    expect(await screen.findByRole('heading', { name: 'API' })).toBeInTheDocument()
  } finally {
    liberar.abrir()
    pararObservacao?.()
    try {
      const r = await montagem
      r?.router.dispose()
    } finally {
      rota.lazy = original
    }
  }
})

it('boot atrasado continua observável no splash da rota eager', async () => {
  const refresh = portao()
  let iniciouRefresh = false
  captura.refreshPreso = refresh.aberto
  captura.observarRefresh = () => { iniciouRefresh = true }
  const montagem = renderizarPainel('/', { atrasoDoBoot: 200 })
  try {
    const r = await montagem
    expect(iniciouRefresh).toBe(true)
    expect(r.router.state.initialized).toBe(true)
    expect(r.sessao.estado()).toBe('verificando')
    expect(screen.getByText('Carregando o StillOn…')).toBeInTheDocument()
    refresh.abrir()
    await act(() => r.sessao.iniciar())
    expect(await screen.findByRole('heading', { name: 'Painel' })).toBeInTheDocument()
  } finally {
    refresh.abrir()
    const r = await montagem
    await act(() => r.sessao.iniciar())
    r.router.dispose()
  }
})

it('import rejeitado chega ao boundary real sem pendurar a fixture', async () => {
  const rota = rotaDoDetalhe()
  const original = rota.lazy
  rota.lazy = async () => { throw new TypeError('Failed to fetch dynamically imported module: /assets/detalhe-antigo.js') }
  let r: Awaited<ReturnType<typeof renderizarPainel>> | undefined
  try {
    r = await renderizarPainel('/targets/alvo-1')
    expect(r.router.state.initialized).toBe(true)
    expect(await screen.findByRole('heading', { name: 'Há uma versão nova do painel' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Recarregar' })).toBeInTheDocument()
  } finally {
    r?.router.dispose()
    rota.lazy = original
  }
})
