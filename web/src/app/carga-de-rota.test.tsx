import { onlineManager } from '@tanstack/react-query'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
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

describe('pré-carga com economia de dados', () => {
  const economia = (saveData: boolean) => Object.defineProperty(navigator, 'connection', { value: { saveData }, configurable: true })
  const ociosoImediato = () =>
    vi.spyOn(window, 'requestIdleCallback').mockImplementation((fn) => {
      fn({ didTimeout: false, timeRemaining: () => 50 })
      return 1
    })

  afterEach(() => {
    Reflect.deleteProperty(navigator, 'connection')
  })

  // Sabotagem: tirar a checagem do saveData.
  it('ligada, nenhum chunk chega em ocioso', async () => {
    economia(true)
    const ocioso = ociosoImediato()
    await renderizarPainel('/channels')
    await screen.findByRole('navigation', { name: 'Principal' })
    expect(ocioso).not.toHaveBeenCalled()
    expect(carregarDetalhe).not.toHaveBeenCalled()
    expect(carregarConfiguracoes).not.toHaveBeenCalled()
  })

  // Sabotagem: não pré-carregar em ocioso em nenhum caso, ou inverter a checagem.
  it('desligada, os dois chunks chegam em ocioso', async () => {
    economia(false)
    ociosoImediato()
    await renderizarPainel('/channels')
    await screen.findByRole('navigation', { name: 'Principal' })
    expect(carregarDetalhe).toHaveBeenCalled()
    expect(carregarConfiguracoes).toHaveBeenCalled()
  })

  // Sabotagem: levar a checagem do saveData para o preCarregar.
  it('ligada, passar o ponteiro na aba ainda pede o chunk', async () => {
    economia(true)
    const r = await renderizarPainel('/channels')
    await r.usuario.hover(await aba('Conta'))
    expect(carregarConfiguracoes).toHaveBeenCalledTimes(1)
  })

  // Sabotagem: levar a checagem do saveData para o preCarregar.
  it('ligada, focar a aba ainda pede o chunk', async () => {
    economia(true)
    await renderizarPainel('/channels')
    fireEvent.focus(await aba('Conta'))
    expect(carregarConfiguracoes).toHaveBeenCalledTimes(1)
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

describe('chunk que falha com rede', () => {
  // Pelas rotas de verdade: uma troca de ramo no boundary, ou tirar o boundary
  // da rota que envolve as lazy, só apareceria aqui. Sabotagem: semRede sem
  // olhar a rede, ou tirar o ErrorBoundary dessa rota.
  it('diz que há uma versão nova, dentro do Layout, e oferece recarregar', async () => {
    vi.mocked(carregarConfiguracoes).mockImplementation(async () => {
      throw new TypeError('Failed to fetch dynamically imported module: /assets/PaginaConfiguracoes-velho.js')
    })
    const r = await renderizarPainel('/channels')
    const navegacao = await screen.findByRole('navigation', { name: 'Principal' })
    await r.usuario.click(await aba('Conta'))
    expect(await screen.findByRole('heading', { name: 'Há uma versão nova do painel' })).toBeInTheDocument()
    expect(navegacao).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Recarregar' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
  })
})

describe('rota sob demanda sem rede', () => {
  afterEach(() => onlineManager.setOnline(true))

  // Sabotagem: tirar o ErrorBoundary da rota que envolve as lazy; o boundary
  // do ExigirSessao desmonta o Layout.
  it('o erro fica dentro do Layout, e tentar de novo traz a tela', async () => {
    let chunkChega = false
    vi.mocked(carregarConfiguracoes).mockImplementation(async () => {
      if (!chunkChega) throw new TypeError('Failed to fetch dynamically imported module: /assets/PaginaConfiguracoes.js')
      return import('@/features/configuracoes/PaginaConfiguracoes')
    })
    const r = await renderizarPainel('/channels')
    const navegacao = await screen.findByRole('navigation', { name: 'Principal' })
    act(() => onlineManager.setOnline(false))
    await r.usuario.click(await aba('Conta'))
    expect(await screen.findByRole('heading', { name: 'Esta tela depende da conexão' })).toBeInTheDocument()
    expect(navegacao).toBeInTheDocument()
    chunkChega = true
    act(() => onlineManager.setOnline(true))
    await r.usuario.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'Esta tela depende da conexão' })).not.toBeInTheDocument())
    expect(navegacao).toBeInTheDocument()
    expect(await aba('Conta')).toHaveAttribute('aria-current', 'page')
  })
})
