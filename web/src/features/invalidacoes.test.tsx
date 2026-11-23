import { hashKey, type QueryClient, type QueryKey } from '@tanstack/react-query'
import { act, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { chaves } from '@/lib/api/consultas'
import { alvo, canal, incidente, itemDoResumo } from '@/test/fixtures'
import { PROJETO_PADRAO, type ApiFalsa } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
// Pré-carrega as rotas lazy e a folha de estilos na coleta, que não tem prazo.
import '@/features/alvos/PaginaDetalhe'
import '@/test/aquecer-estilos'

type Chamada = { metodo: 'invalidateQueries' | 'removeQueries'; queryKey: QueryKey | undefined; vivas: number }

// Anota cada invalidação/remoção do cliente e quantas queries do cache ela
// alcançava na hora. Chave trocada não dá erro: casa com zero queries.
function espiarInvalidacoes(cliente: QueryClient) {
  const chamadas: Chamada[] = []
  const anotar = (metodo: Chamada['metodo'], filtro: { queryKey?: QueryKey } | undefined) =>
    chamadas.push({ metodo, queryKey: filtro?.queryKey, vivas: cliente.getQueryCache().findAll(filtro).length })
  const invalidar = cliente.invalidateQueries.bind(cliente)
  cliente.invalidateQueries = ((filtro, opcoes) => {
    anotar('invalidateQueries', filtro)
    return invalidar(filtro, opcoes)
  }) as QueryClient['invalidateQueries']
  const remover = cliente.removeQueries.bind(cliente)
  cliente.removeQueries = ((filtro) => {
    anotar('removeQueries', filtro)
    remover(filtro)
  }) as QueryClient['removeQueries']

  const achar = (metodo: Chamada['metodo'], chave: QueryKey) =>
    chamadas.find((c) => c.metodo === metodo && c.queryKey !== undefined && hashKey(c.queryKey) === hashKey(chave))
  // Espera a chamada, de chave exatamente igual à da fábrica, e devolve
  // quantas queries do cache ela alcançava.
  async function vivas(metodo: Chamada['metodo'], chave: QueryKey) {
    await waitFor(() => expect(achar(metodo, chave), `${metodo}(${hashKey(chave)}) não foi chamada`).toBeDefined())
    return achar(metodo, chave)?.vivas ?? 0
  }
  return { vivas }
}

// As telas abaixo moram no projeto padrão.
const doPadrao = chaves.doProjeto(PROJETO_PADRAO)

describe('cada invalidação casa com uma query viva, pela chave da fábrica', () => {
  it('incidentes: reconhecer', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(await screen.findByRole('button', { name: 'Reconhecer o incidente de API' }))
    expect(await e.vivas('invalidateQueries', doPadrao.todosOsIncidentes)).toBeGreaterThan(0)
  })

  it('faixa do alvo: reconhecer', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
    r.falsa.estado.incidentes = [incidente({ cause: 'conn_refused' })]
    await r.cliente.refetchQueries()
    const faixa = await screen.findByRole('region', { name: /^Fora do ar há / })
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(within(faixa).getByRole('button', { name: 'Reconhecer' }))
    expect(await e.vivas('invalidateQueries', doPadrao.todosOsIncidentes)).toBeGreaterThan(0)
  })

  it('alvo: pausar', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const botao = await screen.findByRole('button', { name: 'Pausar' })
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(botao)
    expect(await e.vivas('invalidateQueries', doPadrao.todosOsAlvos)).toBeGreaterThan(0)
  })

  it('alvo: apagar remove o alvo e atualiza os alvos e os incidentes', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    const dialogo = await screen.findByRole('alertdialog')
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Apagar alvo' }))
    expect(await e.vivas('removeQueries', doPadrao.alvo('alvo-1'))).toBeGreaterThan(0)
    expect(await e.vivas('invalidateQueries', doPadrao.todosOsAlvos)).toBeGreaterThan(0)
    expect(await e.vivas('invalidateQueries', doPadrao.todosOsIncidentes)).toBeGreaterThan(0)
  })

  it('formulário: cadastrar', async () => {
    const r = await renderizarPainel('/targets/new')
    // O formulário sozinho não carrega query de alvos; na vida real o resumo
    // vem da lista, visitada antes.
    await r.cliente.prefetchQuery({ queryKey: doPadrao.resumo, queryFn: () => ({ items: [] }) })
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    expect(await e.vivas('invalidateQueries', doPadrao.todosOsAlvos)).toBeGreaterThan(0)
  })

  it('formulário: salvar', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const nome = await screen.findByLabelText('Nome')
    await r.usuario.clear(nome)
    await r.usuario.type(nome, 'API nova')
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await e.vivas('invalidateQueries', doPadrao.todosOsAlvos)).toBeGreaterThan(0)
  })

  it('canais: pausar', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }))
    const confirmar = await screen.findByRole('alertdialog')
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(within(confirmar).getByRole('button', { name: 'Pausar canal' }))
    expect(await e.vivas('invalidateQueries', doPadrao.canais)).toBeGreaterThan(0)
  })

  it('status page: criar', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Título'), 'Minha Empresa')
    await r.usuario.tab() // o blur sugere o slug
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    expect(await e.vivas('invalidateQueries', doPadrao.paginas)).toBeGreaterThan(0)
  })
})

// criarProjeto numera pelo tamanho da lista: depois do padrão, proj-2 e proj-3.
const A = 'proj-2'
const B = 'proj-3'

function comDoisProjetos(falsa: ApiFalsa) {
  falsa.criarProjeto('A')
  falsa.criarProjeto('B')
}

const recursosDe = (falsa: ApiFalsa, id: string) => {
  const p = falsa.estado.projetos.find((x) => x.projeto.id === id)
  if (!p) throw new Error(`sem o projeto ${id}`)
  return p.recursos
}

describe('as invalidações ficam no projeto', () => {
  // Sabotagem: o cadastro invalida todos os projetos → o resumo de B fica
  // velho e é pedido de novo na volta a B.
  it('criar alvo em A não refaz o GET de B', async () => {
    const r = await renderizarPainel(`/projects/${B}/`, { antes: comDoisProjetos })
    await screen.findByRole('heading', { level: 1, name: 'Painel' })
    await waitFor(() => expect(recursosDe(r.falsa, B).consultasDoResumo.length).toBeGreaterThan(0))
    const pedidosDeB = recursosDe(r.falsa, B).consultasDoResumo.length

    await act(() => r.router.navigate(`/projects/${A}/targets/new`))
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${A}/targets/alvo-1`))
    expect(r.cliente.getQueryState(chaves.doProjeto(B).resumo)?.isInvalidated).toBe(false)

    await act(() => r.router.navigate(`/projects/${B}/`))
    await screen.findByRole('heading', { level: 1, name: 'Painel' })
    await act(() => new Promise((resolve) => setTimeout(resolve, 100)))
    expect(recursosDe(r.falsa, B).consultasDoResumo).toHaveLength(pedidosDeB)
  })

  // Sabotagem: o contador da aba com a chave sem o projeto → o "1 aberto" de
  // B continua na aba em A.
  it('incidente aberto só em B não põe contador na aba Incidentes de A', async () => {
    const r = await renderizarPainel(`/projects/${B}/`, {
      antes: (falsa) => {
        comDoisProjetos(falsa)
        recursosDe(falsa, B).incidentes = [incidente()]
      },
    })
    const nav = await screen.findByRole('navigation', { name: 'Principal' })
    expect(await within(nav).findByRole('link', { name: 'Incidentes, 1 aberto' })).toBeInTheDocument()

    await act(() => r.router.navigate(`/projects/${A}/`))
    await waitFor(() => expect(within(nav).getByRole('link', { name: 'Incidentes' })).toHaveAttribute('href', `/projects/${A}/incidents`))
    await act(() => new Promise((resolve) => setTimeout(resolve, 100)))
    expect(within(nav).queryByRole('link', { name: /^Incidentes, / })).not.toBeInTheDocument()
  })

  // Sabotagem: o cadastro pela API da raiz, ou navegando a /targets/{id} →
  // o alvo nasce no padrão, ou a tela vai ao padrão.
  it('criar em /projects/B/targets/new cria em B e navega para /projects/B/targets/{id}', async () => {
    const r = await renderizarPainel(`/projects/${B}/targets/new`, { antes: comDoisProjetos })
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe(`/projects/${B}/targets/alvo-1`))
    expect(recursosDe(r.falsa, B).alvos.map((a) => a.name)).toEqual(['Site'])
    expect(r.falsa.estado.alvos).toEqual([])
  })
})
