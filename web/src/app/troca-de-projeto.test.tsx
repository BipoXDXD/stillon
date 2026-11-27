import { act, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { itemDoResumo } from '@/test/fixtures'
import type { ApiFalsa } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { caminhoNoProjeto } from './projeto'

// criarProjeto numera pelo tamanho da lista: depois do padrão, proj-2 e proj-3.
const A = 'proj-2'
const B = 'proj-3'

function comDoisProjetos(falsa: ApiFalsa) {
  falsa.criarProjeto('A').recursos.resumo = [itemDoResumo({ id: 'a1', name: 'api-a' })]
  falsa.criarProjeto('B').recursos.resumo = [itemDoResumo({ id: 'b1', name: 'api-b' })]
}

const recursosDe = (falsa: ApiFalsa, id: string) => {
  const p = falsa.estado.projetos.find((x) => x.projeto.id === id)
  if (!p) throw new Error(`sem o projeto ${id}`)
  return p.recursos
}

// Anota se o texto apareceu em algum momento, mesmo que por um render só:
// esperar e olhar no fim deixaria passar um piscar do projeto errado.
function vigiarTexto(texto: string) {
  let visto = false
  const olhar = () => {
    if (document.body.textContent?.includes(texto)) visto = true
  }
  const observador = new MutationObserver(olhar)
  observador.observe(document.body, { childList: true, subtree: true, characterData: true })
  olhar()
  return {
    visto: () => visto,
    parar: () => observador.disconnect(),
  }
}

describe('troca de projeto', () => {
  // Foco #5 da revisão do plano: o painel mostra dados de um projeto na tela
  // de outro (chave de cache sem projeto, ou a resposta em voo de A chegando
  // depois da troca para B). Sabotagem: uma consulta de recurso com a chave
  // sem o projeto → "api-a" aparece em B.
  it('as listas mudam com o projeto', async () => {
    const r = await renderizarPainel(`/projects/${A}/`, { antes: comDoisProjetos })
    expect(await screen.findByText('api-a')).toBeInTheDocument()

    // Uma releitura de A em voo, que só responde depois da troca.
    recursosDe(r.falsa, A).atrasoMs = 500
    void r.cliente.invalidateQueries()
    await act(() => r.router.navigate(`/projects/${B}/`))
    const depois = vigiarTexto('api-a')
    expect(await screen.findByText('api-b')).toBeInTheDocument()
    // A resposta de A chega aqui, já com B na tela.
    await act(() => new Promise((resolve) => setTimeout(resolve, 700)))
    expect(screen.getByText('api-b')).toBeInTheDocument()
    expect(depois.visto(), '"api-a" apareceu na tela de B').toBe(false)
    depois.parar()
  })

  // Uma chave sem o projeto pode não piscar na tela (a tabela espera outras
  // consultas do projeto), mas serviria o cache de A a B. Sabotagem: a chave
  // do resumo sem o projeto → ela aparece aqui.
  it('toda consulta viva em B é de B', async () => {
    const r = await renderizarPainel(`/projects/${A}/`, { antes: comDoisProjetos })
    expect(await screen.findByText('api-a')).toBeInTheDocument()
    await act(() => r.router.navigate(`/projects/${B}/`))
    expect(await screen.findByText('api-b')).toBeInTheDocument()
    const DA_CONTA = new Set(['me', 'projects', 'sessions'])
    const deFora = r.cliente
      .getQueryCache()
      .findAll({ type: 'active' })
      .map((q) => q.queryKey)
      .filter((chave) => !DA_CONTA.has(String(chave[0])) && !(chave[0] === 'projeto' && chave[1] === B))
    expect(deFora).toEqual([])
  })

  // O estado local de uma tela de A não segue para B: a subárvore do projeto
  // remonta na troca. Sabotagem: Outlet sem key → o nome digitado em A
  // continua no campo em B.
  it('o que se digitou em A não aparece em B', async () => {
    const r = await renderizarPainel(`/projects/${A}/targets/new`, { antes: comDoisProjetos })
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Rascunho de A')
    await act(() => r.router.navigate(`/projects/${B}/targets/new`))
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${B}/targets/new`))
    expect(await screen.findByLabelText('Nome')).toHaveValue('')
  })
})

describe('caminhoNoProjeto', () => {
  // Trocar de projeto mantém a seção; um detalhe volta à raiz da seção.
  it.each([
    [`/projects/${A}/`, `/projects/${B}/`],
    [`/projects/${A}/incidents`, `/projects/${B}/incidents`],
    [`/projects/${A}/channels`, `/projects/${B}/channels`],
    [`/projects/${A}/status-pages`, `/projects/${B}/status-pages`],
    [`/projects/${A}/targets/a1`, `/projects/${B}/`],
    [`/projects/${A}/targets/a1/edit`, `/projects/${B}/`],
    [`/projects/${A}/heartbeats/h1`, `/projects/${B}/`],
    ['/settings', `/projects/${B}/`],
  ])('de %s vai a %s', (de, para) => {
    expect(caminhoNoProjeto(de, B)).toBe(para)
  })
})
