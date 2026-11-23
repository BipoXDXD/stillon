import { act, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PROJETO_PADRAO, type ApiFalsa } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { CHAVE_DO_ULTIMO_PROJETO } from './escolher-projeto'

// Os prefixos das rotas de recurso, nas duas formas (a da raiz sai na Task 30).
const RECURSOS = /^[A-Z]+ (\/projects\/:projectId)?\/(targets|heartbeats|incidents|channels|status-pages)\b/
// As consultas da moldura (o contador de abertos e as tarefas que pedem
// atenção): o Layout as faz na raiz, em qualquer tela, até a Task 30.
const DA_MOLDURA = new Set(['GET /incidents', 'GET /heartbeats'])

function pedidosDeRecurso(falsa: ApiFalsa): string[] {
  return [...falsa.estado.chamadas.keys()].filter((k) => RECURSOS.test(k) && !DA_MOLDURA.has(k))
}

const comProjetoB = (falsa: ApiFalsa) => {
  falsa.criarProjeto('B')
}
// criarProjeto numera pelo tamanho da lista: o segundo projeto é proj-2.
const PROJETO_B = 'proj-2'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('projeto atual na URL', () => {
  it('a raiz vai ao projeto padrão e preserva a search', async () => {
    const r = await renderizarPainel('/?estado=down')
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${PROJETO_PADRAO}/`))
    expect(r.router.state.location.search).toBe('?estado=down')
  })

  it('depois de abrir um projeto, a raiz volta a ele', async () => {
    const primeira = await renderizarPainel(`/projects/${PROJETO_B}/`, { antes: comProjetoB })
    await screen.findByRole('heading', { level: 1, name: 'Painel' })
    await waitFor(() => expect(localStorage.getItem(CHAVE_DO_ULTIMO_PROJETO)).toBe(PROJETO_B))
    primeira.tela.unmount()

    const r = await renderizarPainel('/', { antes: comProjetoB })
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${PROJETO_B}/`))
  })

  it('com o localStorage lançando exceção, a raiz vai ao padrão', async () => {
    localStorage.setItem(CHAVE_DO_ULTIMO_PROJETO, PROJETO_B)
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('bloqueado', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('bloqueado', 'SecurityError')
    })
    const r = await renderizarPainel('/', { antes: comProjetoB })
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${PROJETO_PADRAO}/`))
    expect(await screen.findByRole('heading', { level: 1, name: 'Painel' })).toBeInTheDocument()
  })

  it('projeto fora da lista mostra a página não encontrada, sem consulta de recurso', async () => {
    const r = await renderizarPainel(`/projects/${crypto.randomUUID()}/`)
    expect(await screen.findByRole('heading', { level: 1, name: 'Página não encontrada' })).toBeInTheDocument()
    expect(r.falsa.chamadas('GET /projects')).toBeGreaterThan(0)
    // Um tempo para um pedido de recurso já disparado chegar à falsa.
    await act(() => new Promise((resolve) => setTimeout(resolve, 100)))
    expect(pedidosDeRecurso(r.falsa)).toEqual([])
  })
})
