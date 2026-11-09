import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { alvo, incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { carregarConfiguracoes, carregarDetalhe, preCarregar, quandoOcioso } from './rotas-sob-demanda'

// As funções de verdade, sob espiões: o teste vê quem pediu o chunk.
vi.mock('./rotas-sob-demanda', async (original) => {
  const r = await original<typeof import('./rotas-sob-demanda')>()
  return {
    ...r,
    carregarConfiguracoes: vi.fn<typeof r.carregarConfiguracoes>(r.carregarConfiguracoes),
    carregarDetalhe: vi.fn<typeof r.carregarDetalhe>(r.carregarDetalhe),
  }
})

const PRAZO_DOS_IMPORTS_MS = 30_000

beforeAll(async () => {
  await Promise.all([import('@/features/alvos/PaginaDetalhe'), import('@/features/configuracoes/PaginaConfiguracoes')])
}, PRAZO_DOS_IMPORTS_MS)

afterEach(() => {
  vi.clearAllMocks()
  vi.restoreAllMocks()
})

const aba = async (nome: string) => within(await screen.findByRole('navigation', { name: 'Principal' })).getByRole('link', { name: nome })

describe('pré-carga do chunk', () => {
  // Sabotagem: tirar o onPointerEnter da aba.
  it('passar o ponteiro na aba Conta pede o chunk', async () => {
    const r = await renderizarPainel('/channels')
    await r.usuario.hover(await aba('Conta'))
    expect(carregarConfiguracoes).toHaveBeenCalledTimes(1)
  })

  // Sabotagem: tirar o onFocus da aba.
  it('focar a aba Conta pede o chunk', async () => {
    await renderizarPainel('/channels')
    fireEvent.focus(await aba('Conta'))
    expect(carregarConfiguracoes).toHaveBeenCalledTimes(1)
  })

  // Sabotagem: tirar o preCarregar do link da linha.
  it('passar o ponteiro no nome do alvo pede o chunk do detalhe', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo()]
    await r.cliente.refetchQueries()
    await r.usuario.hover(await screen.findByRole('link', { name: 'API' }))
    expect(carregarDetalhe).toHaveBeenCalledTimes(1)
  })

  // Sabotagem: tirar o aoApontarPreCarregar do link do cartão.
  it('passar o ponteiro no alvo de um incidente pede o chunk do detalhe', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    await r.usuario.hover(await screen.findByRole('link', { name: 'API' }))
    expect(carregarDetalhe).toHaveBeenCalledTimes(1)
  })

  // Toque não tem hover. Sabotagem: tirar o efeito de ocioso do Layout.
  it('com o navegador ocioso, os dois chunks chegam sem hover', async () => {
    vi.spyOn(window, 'requestIdleCallback').mockImplementation((fn) => {
      fn({ didTimeout: false, timeRemaining: () => 50 })
      return 1
    })
    await renderizarPainel('/channels')
    await screen.findByRole('navigation', { name: 'Principal' })
    expect(carregarDetalhe).toHaveBeenCalled()
    expect(carregarConfiguracoes).toHaveBeenCalled()
  })

  // Sabotagem: tirar o .catch: a rejeição sem dono reprova a suíte inteira.
  it('chunk que não vem (deploy novo trocou o hash) não vira erro', async () => {
    await expect(Promise.resolve(preCarregar(() => Promise.reject(new Error('chunk sumiu'))))).resolves.toBeUndefined()
  })
})

describe('quandoOcioso', () => {
  it('usa o requestIdleCallback, e cancela por ele', () => {
    const pedir = vi.spyOn(window, 'requestIdleCallback').mockReturnValue(7)
    const cancelar = vi.spyOn(window, 'cancelIdleCallback')
    const parar = quandoOcioso(() => {})
    expect(pedir).toHaveBeenCalledTimes(1)
    parar()
    expect(cancelar).toHaveBeenCalledWith(7)
  })

  // O Safari não tem requestIdleCallback, e o toque é onde a rede costuma ser
  // pior. Sabotagem: sem o ramo do setTimeout.
  it('sem requestIdleCallback, roda depois de uma folga', () => {
    vi.useFakeTimers()
    const original = window.requestIdleCallback
    Reflect.deleteProperty(window, 'requestIdleCallback')
    try {
      const feito = vi.fn<() => void>()
      quandoOcioso(feito)
      vi.advanceTimersByTime(1999)
      expect(feito).not.toHaveBeenCalled()
      vi.advanceTimersByTime(1)
      expect(feito).toHaveBeenCalledTimes(1)
    } finally {
      window.requestIdleCallback = original
      vi.useRealTimers()
    }
  })
})

describe('espera por uma rota sob demanda', () => {
  function chunkSegurado() {
    let soltar = () => {}
    const chegou = new Promise<void>((resolve) => {
      soltar = resolve
    })
    vi.mocked(carregarConfiguracoes).mockImplementation(async () => {
      await chegou
      return import('@/features/configuracoes/PaginaConfiguracoes')
    })
    return soltar
  }

  // Sabotagem: destacar pelo pathname atual; o item só muda ao chegar o chunk.
  it('marca o item no clique, sem afirmar aria-current antes da hora', async () => {
    const soltar = chunkSegurado()
    const r = await renderizarPainel('/channels')
    const conta = await aba('Conta')
    await r.usuario.click(conta)
    expect(conta).toHaveClass('bg-primaria-100')
    expect(conta).not.toHaveAttribute('aria-current')
    expect(await aba('Canais')).toHaveAttribute('aria-current', 'page')
    expect(await aba('Canais')).not.toHaveClass('bg-primaria-100')
    soltar()
    await waitFor(() => expect(conta).toHaveAttribute('aria-current', 'page'))
  })

  // O atraso em si está em lib/atraso.test.ts. Sabotagem: tirar o filete, ou
  // tirar o anúncio, ou usar a classe animada sem o motion-reduce.
  it('passados 300 ms, filete de 2 px no topo; passado 1 s, anúncio; ao chegar, some', async () => {
    const soltar = chunkSegurado()
    const r = await renderizarPainel('/channels')
    await r.usuario.click(await aba('Conta'))
    const filete = await screen.findByTestId('filete-de-carga', {}, { timeout: 3000 })
    expect(filete).toHaveClass('h-0.5', 'bg-primaria', 'motion-reduce:animate-none', 'fixed', 'top-0')
    expect(filete).toHaveAttribute('aria-hidden', 'true')
    await waitFor(() => expect(screen.getByTestId('anuncios')).toHaveTextContent('Carregando a tela…'), { timeout: 3000 })
    soltar()
    await waitFor(() => expect(screen.queryByTestId('filete-de-carga')).not.toBeInTheDocument())
  })
})
