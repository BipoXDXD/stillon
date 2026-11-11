import { hashKey, type QueryClient, type QueryKey } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { chaves } from '@/lib/api/consultas'
import { alvo, canal, incidente, itemDoResumo } from '@/test/fixtures'
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

describe('cada invalidação casa com uma query viva, pela chave da fábrica', () => {
  it('incidentes: reconhecer', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(await screen.findByRole('button', { name: 'Reconhecer o incidente de API' }))
    expect(await e.vivas('invalidateQueries', chaves.todosOsIncidentes)).toBeGreaterThan(0)
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
    expect(await e.vivas('invalidateQueries', chaves.todosOsIncidentes)).toBeGreaterThan(0)
  })

  it('alvo: pausar', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const botao = await screen.findByRole('button', { name: 'Pausar' })
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(botao)
    expect(await e.vivas('invalidateQueries', chaves.todosOsAlvos)).toBeGreaterThan(0)
  })

  it('alvo: apagar remove o alvo e atualiza os alvos e os incidentes', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    const dialogo = await screen.findByRole('alertdialog')
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Apagar alvo' }))
    expect(await e.vivas('removeQueries', chaves.alvo('alvo-1'))).toBeGreaterThan(0)
    expect(await e.vivas('invalidateQueries', chaves.todosOsAlvos)).toBeGreaterThan(0)
    expect(await e.vivas('invalidateQueries', chaves.todosOsIncidentes)).toBeGreaterThan(0)
  })

  it('formulário: cadastrar', async () => {
    const r = await renderizarPainel('/targets/new')
    // O formulário sozinho não carrega query de alvos; na vida real o resumo
    // vem da lista, visitada antes.
    await r.cliente.prefetchQuery({ queryKey: chaves.resumo, queryFn: () => ({ items: [] }) })
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    expect(await e.vivas('invalidateQueries', chaves.todosOsAlvos)).toBeGreaterThan(0)
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
    expect(await e.vivas('invalidateQueries', chaves.todosOsAlvos)).toBeGreaterThan(0)
  })

  it('canais: pausar', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }))
    const confirmar = await screen.findByRole('alertdialog')
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(within(confirmar).getByRole('button', { name: 'Pausar canal' }))
    expect(await e.vivas('invalidateQueries', chaves.canais)).toBeGreaterThan(0)
  })

  it('status page: criar', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Título'), 'Minha Empresa')
    await r.usuario.tab() // o blur sugere o slug
    const e = espiarInvalidacoes(r.cliente)
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    expect(await e.vivas('invalidateQueries', chaves.paginas)).toBeGreaterThan(0)
  })
})
