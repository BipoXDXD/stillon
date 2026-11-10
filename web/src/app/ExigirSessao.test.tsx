import { setImmediate as concluirTurno } from 'node:timers/promises'
import { act, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, it, vi } from 'vitest'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

// A API e a sessão reais da fixture permanecem em uso. Só a primeira
// resposta de rede é interceptada antes do boot, na fronteira pública.
vi.mock('@/test/ambiente', async (original) => {
  const modulo = await original<typeof import('@/test/ambiente')>()
  return {
    ...modulo,
    usarApiFalsa: () => {
      const falsa = modulo.usarApiFalsa()
      servidor.use(http.post('*/api/v1/auth/refresh', () => HttpResponse.error()))
      return falsa
    },
  }
})

function prenderRefresh() {
  let abrir!: () => void
  let entrou!: () => void
  const liberado = new Promise<void>((resolve) => { abrir = resolve })
  const recebido = new Promise<void>((resolve) => { entrou = resolve })
  servidor.use(http.post('*/api/v1/auth/refresh', async () => {
    entrou()
    await liberado
    // Fallthrough público: o handler original seguinte produz a resposta.
    return undefined
  }, { once: true }))
  return { abrir, recebido }
}

function observarRefresh() {
  let quantidade = 0
  const observar = ({ request }: { request: Request }) => {
    if (request.method === 'POST' && new URL(request.url).pathname === '/api/v1/auth/refresh') quantidade++
  }
  servidor.events.on('request:start', observar)
  return {
    quantidade: () => quantidade,
    parar: () => servidor.events.removeListener('request:start', observar),
  }
}

it('Tentar de novo mostra splash e recupera o destino original quando a rede volta', async () => {
  const requisicoes = observarRefresh()
  let r: Awaited<ReturnType<typeof renderizarPainel>> | undefined
  let portao: ReturnType<typeof prenderRefresh> | undefined
  try {
    r = await renderizarPainel('/targets/new?origem=porta')
    expect(screen.getByRole('heading')).toHaveTextContent('Não foi possível falar com o servidor')
    expect(r.sessao.estado()).toBe('offline')
    expect(requisicoes.quantidade()).toBe(1)
    // Handlers originais têm precedência sobre a falha inicial; o cookie
    // válido do boot nunca foi consumido pela resposta de erro de rede.
    servidor.use(...r.falsa.handlers)
    portao = prenderRefresh()
    await r.usuario.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    await concluirTurno()
    expect(requisicoes.quantidade()).toBe(2)
    await portao.recebido
    expect(r.sessao.estado()).toBe('verificando')
    // O texto de carregamento é um status; o <main>, que recebe o foco, não
    // fica aria-busy (o leitor de tela calaria o que está nele). Sabotagem:
    // voltar o aria-busy ao <main>, ou o texto a um <p> sem papel.
    expect(screen.getByRole('status')).toHaveTextContent('Carregando o StillOn…')
    // O botão sumiu com o clique: o foco fica no splash, e não no <body>.
    expect(screen.getByRole('main')).toHaveFocus()
    expect(screen.getByRole('main')).not.toHaveAttribute('aria-busy')
    expect(screen.queryByRole('heading', { name: 'Entrar no StillOn' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Novo alvo' })).not.toBeInTheDocument()
    expect(r.router.state.location.pathname).toBe('/targets/new')
    expect(r.router.state.location.search).toBe('?origem=porta')
    portao.abrir()
    await act(() => r!.sessao.iniciar())
    expect(r.sessao.estado()).toBe('autenticado')
    expect(await screen.findByRole('heading', { name: 'Novo alvo' })).toHaveFocus()
    expect(r.router.state.location.pathname).toBe('/targets/new')
    expect(r.router.state.location.search).toBe('?origem=porta')
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(1)
  } finally {
    portao?.abrir()
    if (r) {
      await act(() => r!.sessao.iniciar())
      r.router.dispose()
      r.tela.unmount()
    }
    requisicoes.parar()
    servidor.resetHandlers()
  }
})

it('nova falha de rede mantém a sessão offline e oferece outra tentativa', async () => {
  const requisicoes = observarRefresh()
  let r: Awaited<ReturnType<typeof renderizarPainel>> | undefined
  let portao: ReturnType<typeof prenderRefresh> | undefined
  try {
    r = await renderizarPainel('/targets/new?origem=porta')
    expect(screen.getByRole('heading')).toHaveTextContent('Não foi possível falar com o servidor')
    portao = prenderRefresh()
    await r.usuario.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    await concluirTurno()
    expect(requisicoes.quantidade()).toBe(2)
    await portao.recebido
    portao.abrir()
    await act(() => r!.sessao.iniciar())
    expect(r.sessao.estado()).toBe('offline')
    // O alerta é só a mensagem: envolver o <h1> focado faria o título ser
    // lido duas vezes. Sabotagem: o role="alert" de volta no <div>.
    const alerta = screen.getByRole('alert')
    expect(alerta).toHaveTextContent('Sua sessão pode estar ativa; tente de novo em instantes.')
    expect(alerta).not.toContainElement(screen.getByRole('heading', { name: 'Não foi possível falar com o servidor' }))
    expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeEnabled()
    expect(screen.getByRole('heading', { name: 'Não foi possível falar com o servidor' })).toHaveFocus()
    expect(screen.queryByRole('heading', { name: 'Entrar no StillOn' })).not.toBeInTheDocument()
    expect(r.router.state.location.pathname).toBe('/targets/new')
    expect(r.router.state.location.search).toBe('?origem=porta')
  } finally {
    portao?.abrir()
    if (r) {
      await act(() => r!.sessao.iniciar())
      r.router.dispose()
      r.tela.unmount()
    }
    requisicoes.parar()
    servidor.resetHandlers()
  }
})

// O foco no <h1> do login vem da reserva do splash: o Titulo do login é a
// primeira tela da carga e não se foca sozinho. Sabotagem conferida: tirar o
// useReservaDeFoco do SplashDoPainel derruba este teste.
it('retry com 401 do servidor leva ao login preservando pathname e query no next', async () => {
  const requisicoes = observarRefresh()
  let r: Awaited<ReturnType<typeof renderizarPainel>> | undefined
  let portao: ReturnType<typeof prenderRefresh> | undefined
  try {
    r = await renderizarPainel('/targets/new?origem=porta', { logado: false })
    expect(screen.getByRole('heading')).toHaveTextContent('Não foi possível falar com o servidor')
    servidor.use(...r.falsa.handlers)
    portao = prenderRefresh()
    await r.usuario.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    await concluirTurno()
    expect(requisicoes.quantidade()).toBe(2)
    await portao.recebido
    portao.abrir()
    await act(() => r!.sessao.iniciar())
    expect(r.sessao.estado()).toBe('anonimo')
    expect(await screen.findByRole('heading', { name: 'Entrar no StillOn' })).toHaveFocus()
    expect(r.router.state.location.pathname).toBe('/login')
    expect(r.router.state.location.search).toBe('?next=%2Ftargets%2Fnew%3Forigem%3Dporta')
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(1)
  } finally {
    portao?.abrir()
    if (r) {
      await act(() => r!.sessao.iniciar())
      r.router.dispose()
      r.tela.unmount()
    }
    requisicoes.parar()
    servidor.resetHandlers()
  }
})
