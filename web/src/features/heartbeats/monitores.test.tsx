import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { heartbeat, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

const tabela = () => screen.findByRole('table', { name: /^Monitores/ })
const nomesNaTabela = async () =>
  within(await tabela())
    .getAllByRole('rowheader')
    .map((c) => within(c).getByRole('link').textContent)

async function painelCom(p: { alvos?: Parameters<typeof itemDoResumo>[0][]; heartbeats?: Parameters<typeof heartbeat>[0][] }) {
  const r = await renderizarPainel('/')
  r.falsa.estado.resumo = (p.alvos ?? []).map(itemDoResumo)
  r.falsa.estado.heartbeats = (p.heartbeats ?? []).map(heartbeat)
  await r.cliente.refetchQueries()
  return r
}

describe('heartbeats na tabela de monitores', () => {
  // Sabotagem: não passar os heartbeats a ResultadoDosFiltros → a linha some.
  it('aparecem junto dos alvos, por gravidade, com o selo da tarefa e link ao detalhe', async () => {
    await painelCom({
      alvos: [{ id: 'a', name: 'API' }],
      heartbeats: [{ id: 'h1', name: 'Backup', state: 'late', deadline_at: '2026-09-24T09:00:00Z' }],
    })
    await screen.findByRole('heading', { level: 2, name: 'Uma tarefa pede atenção.' })
    expect(await nomesNaTabela()).toEqual(['Backup', 'API'])
    const linha = within(await tabela()).getByRole('row', { name: /Backup/ })
    expect(within(linha).getByRole('link', { name: 'Backup' })).toHaveAttribute('href', '/heartbeats/h1')
    expect(within(linha).getAllByRole('cell')[0]).toHaveTextContent('Atrasado')
    expect(linha).not.toHaveTextContent('Fora do ar')
  })

  // Critério da spec da tela: a manchete não fica verde com heartbeat fora.
  // Sabotagem: manchete só com os alvos. O filtro tem o teste seguinte.
  it('a manchete e a linha de detalhe falam da tarefa com falha', async () => {
    await painelCom({
      alvos: [{ id: 'a', name: 'API', tags: ['prod'] }],
      heartbeats: [{ id: 'h1', name: 'Backup', state: 'failing' }],
    })
    await screen.findByRole('heading', { level: 2, name: 'Uma tarefa pede atenção.' })
    expect(screen.getByText('1 no ar. Tarefas: 1 com falha.')).toBeInTheDocument()
  })

  // Sabotagem: contar só os alvos → a tag que só o heartbeat tem não vira botão.
  it('a tag que só o heartbeat tem vira filtro, e o filtro acha ele', async () => {
    const r = await painelCom({ alvos: [{ id: 'a', name: 'API', tags: ['prod'] }], heartbeats: [{ id: 'h1', name: 'Backup', tags: ['dados'] }] })
    const barra = await screen.findByRole('region', { name: 'Filtros' })
    await r.usuario.click(await within(barra).findByRole('button', { name: /dados/ }))
    expect(await screen.findByRole('link', { name: 'Backup' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'API' })).not.toBeInTheDocument()
  })

  // Só a posição muda: Banco, fora do ar, continua na tabela e na manchete.
  it('ordenar por nome muda a tabela, vai à URL e não mexe na manchete', async () => {
    const r = await painelCom({
      alvos: [
        { id: 'a', name: 'API' },
        { id: 'b', name: 'Banco', state: 'down' },
      ],
      heartbeats: [{ id: 'h1', name: 'Atualização', tags: [] }],
    })
    await screen.findByRole('heading', { level: 2, name: 'Um serviço pede atenção.' })
    expect(await nomesNaTabela()).toEqual(['Banco', 'API', 'Atualização'])

    await r.usuario.selectOptions(screen.getByLabelText('Ordenar por'), 'Nome')

    await waitFor(() => expect(screen.getByLabelText('Ordenar por')).toHaveValue('nome'))
    expect(await nomesNaTabela()).toEqual(['API', 'Atualização', 'Banco'])
    expect(r.router.state.location.search).toBe('?ordem=nome')
    expect(screen.getByRole('heading', { level: 2, name: 'Um serviço pede atenção.' })).toBeInTheDocument()
  })

  it('a ordem da URL vale ao abrir, e sobrevive a filtrar e a limpar', async () => {
    const r = await renderizarPainel('/?ordem=nome')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'b', name: 'Banco', state: 'down', tags: ['prod'] }),
      itemDoResumo({ id: 'a', name: 'API', tags: ['prod'] }),
    ]
    await r.cliente.refetchQueries()
    await waitFor(async () => expect(await nomesNaTabela()).toEqual(['API', 'Banco']))
    expect(screen.getByLabelText('Ordenar por')).toHaveValue('nome')

    const barra = await screen.findByRole('region', { name: 'Filtros' })
    await r.usuario.click(within(barra).getByRole('button', { name: /prod/ }))
    await waitFor(() => expect(r.router.state.location.search).toBe('?tag=prod&ordem=nome'))
    await r.usuario.click(await within(barra).findByRole('button', { name: 'Limpar filtros' }))
    await waitFor(() => expect(r.router.state.location.search).toBe('?ordem=nome'))
  })

  it('escolher a ordem não mostra "Limpar filtros": ordem não é filtro', async () => {
    const r = await painelCom({ alvos: [{ id: 'a', name: 'API' }] })
    await r.usuario.selectOptions(await screen.findByLabelText('Ordenar por'), 'Nome')
    await waitFor(() => expect(screen.getByLabelText('Ordenar por')).toHaveValue('nome'))
    expect(r.router.state.location.search).toBe('?ordem=nome')
    const barra = screen.getByRole('region', { name: 'Filtros' })
    expect(within(barra).queryByRole('button', { name: 'Limpar filtros' })).not.toBeInTheDocument()
  })

  it('filtro de tag tira o heartbeat sem a tag da tabela, e a manchete fica', async () => {
    const r = await renderizarPainel('/?tag=prod')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', tags: ['prod'] })]
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', state: 'late' })]
    await r.cliente.refetchQueries()
    await screen.findByRole('heading', { level: 2, name: 'Uma tarefa pede atenção.' })
    expect(await nomesNaTabela()).toEqual(['API'])
    expect(screen.getByText('1 de 2 monitores')).toBeInTheDocument()
  })

  // Sabotagem: contar só os alvos na barra → não há botão "Fora do ar".
  it('a barra de filtros conta o heartbeat, e o filtro de estado o alcança', async () => {
    const r = await renderizarPainel('/?estado=down')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API' })]
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', state: 'late' })]
    await r.cliente.refetchQueries()
    const botao = await within(await screen.findByRole('group', { name: 'Estado' })).findByRole('button', { name: /^Fora do ar/ })
    expect(botao).toHaveTextContent('Fora do ar1')
    expect(await nomesNaTabela()).toEqual(['Backup'])
  })

  // Sem alvos e só com heartbeats, o painel não diz "Nenhum serviço vigiado".
  // Sabotagem: manter itens.length > 0 → a tabela não aparece.
  it('só com heartbeats, a tabela aparece', async () => {
    await painelCom({ heartbeats: [{ id: 'h1', name: 'Backup' }] })
    await screen.findByRole('heading', { level: 2, name: 'Tudo no ar.' })
    expect(await nomesNaTabela()).toEqual(['Backup'])
  })

  // Não saber não é "tudo no ar". Sabotagem: manchete com confirmadas: true fixo.
  it('consulta de heartbeats falhou: nada de "Tudo no ar.", com aviso e Tentar de novo', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API' })]
    r.falsa.estado.forcar.set('GET /heartbeats', { status: 500, code: 'internal_error' })
    await r.cliente.resetQueries()
    await screen.findByRole('heading', { level: 2, name: 'Não foi possível conferir as tarefas.' })
    expect(screen.queryByRole('heading', { level: 2, name: 'Tudo no ar.' })).not.toBeInTheDocument()
    expect(await nomesNaTabela()).toEqual(['API'])

    r.falsa.estado.forcar.delete('GET /heartbeats')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', state: 'late' })]
    await r.usuario.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    await screen.findByRole('heading', { level: 2, name: 'Uma tarefa pede atenção.' })
  })

  // A resposta das tarefas pode vir depois da do resumo, e nesse intervalo o
  // painel não sabe. Sabotagem: não esperar por heartbeats.isPending.
  it('até a primeira resposta das tarefas, a manchete não diz "Tudo no ar."', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API' })]
    let soltar = () => {}
    const resposta = new Promise<void>((resolve) => {
      soltar = resolve
    })
    servidor.use(
      http.get('*/api/v1/heartbeats', async () => {
        await resposta
        return HttpResponse.json({ items: [heartbeat({ state: 'late' })] })
      }),
    )
    r.cliente.removeQueries({ queryKey: ['heartbeats'] })
    await r.router.navigate('/')
    await waitFor(() => expect(r.cliente.getQueryData(['targets', 'summary'])).toBeDefined())
    expect(screen.queryByRole('heading', { level: 2 })).not.toBeInTheDocument()
    soltar()
    await screen.findByRole('heading', { level: 2, name: 'Uma tarefa pede atenção.' })
  })

  it('um poll que falha mantém a tarefa atrasada na tabela e na manchete', async () => {
    const r = await painelCom({ heartbeats: [{ id: 'h1', name: 'Backup', state: 'late' }] })
    await screen.findByRole('heading', { level: 2, name: 'Uma tarefa pede atenção.' })
    r.falsa.estado.forcar.set('GET /heartbeats', { status: 500, code: 'internal_error' })
    await r.cliente.refetchQueries({ queryKey: ['heartbeats'] })
    await screen.findByText(/Não foi possível conferir as tarefas agora/)
    expect(screen.getByRole('heading', { level: 2, name: 'Uma tarefa pede atenção.' })).toBeInTheDocument()
    expect(await nomesNaTabela()).toEqual(['Backup'])
    expect(screen.queryByText(/tarefas? em dia/)).not.toBeInTheDocument()
  })

  // O painel pede 100 e mostra o que veio: com a página exatamente cheia, o
  // servidor manda next_cursor e nada busca a página seguinte.
  // Sabotagem: seguir o next_cursor em api.heartbeats → sai um GET com cursor.
  it('com exatamente 100 tarefas, mostra as 100 e ignora o next_cursor', async () => {
    const r = await painelCom({ heartbeats: Array.from({ length: 100 }, (_, i) => ({ id: `h-${i + 1}`, name: `Tarefa ${i + 1}` })) })
    await waitFor(async () => expect(await nomesNaTabela()).toHaveLength(100))
    expect(r.cliente.getQueryData<{ next_cursor?: string }>(['heartbeats'])?.next_cursor).toBe('h-100')
    const consultas = r.falsa.estado.corpos.get('GET /heartbeats') ?? []
    expect(consultas.length).toBeGreaterThan(0)
    for (const consulta of consultas) expect(consulta).toEqual({ limit: '100' })
  })

  it('só heartbeats e a consulta falhando: nada de parágrafo vazio', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.forcar.set('GET /heartbeats', { status: 500, code: 'internal_error' })
    await r.cliente.resetQueries()
    const titulo = await screen.findByRole('heading', { level: 2, name: 'Não foi possível conferir as tarefas.' })
    expect(titulo.closest('div')?.parentElement?.querySelector('p.text-suave')).toBeNull()
  })
})

describe('detalhe do heartbeat', () => {
  // Sabotagem: mostrar o texto do estado exibido → "Fora do ar".
  it('mostra o estado com a palavra da tarefa, o agendamento e os sinais', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.heartbeats = [
      heartbeat({ id: 'h1', name: 'Backup', state: 'late', schedule: { kind: 'cron', expr: '0 3 * * *', timezone: 'America/Sao_Paulo' }, last_ping_at: null, last_success_at: null }),
    ]
    await r.router.navigate('/heartbeats/h1')
    expect(await screen.findByRole('heading', { level: 1, name: 'Backup' })).toBeInTheDocument()
    expect(screen.getAllByText('Atrasado').length).toBeGreaterThan(0)
    expect(screen.queryByText('Fora do ar')).not.toBeInTheDocument()
    expect(screen.getByText('cron 0 3 * * *, America/Sao_Paulo')).toBeInTheDocument()
    expect(screen.getByText('15 min')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Painel', current: 'page' })).toBeInTheDocument()
  })

  // Editar um atrasado põe o prazo à frente e o incidente fica. O fixture vive
  // em 2026-09: 2099 é futuro, 2020 é passado, com qualquer relógio.
  // Sabotagem: tirar a guarda → "o prazo era" com data que não chegou.
  it.each([
    ['vencido', '2020-01-01T12:00:00Z', 'Prazo vencido', /o prazo era/],
    ['futuro', '2099-01-01T12:00:00Z', 'Próximo prazo', /segue aberto até um sinal de sucesso/],
  ])('atrasado com prazo %s', async (_, deadline_at, rotulo, frase) => {
    const r = await renderizarPainel('/')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', state: 'late', deadline_at })]
    await r.router.navigate('/heartbeats/h1')
    expect(await screen.findByText(rotulo)).toBeInTheDocument()
    expect(screen.getByText(frase)).toBeInTheDocument()
  })

  it('pausado não afirma prazo nenhum', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', state: 'paused', enabled: false })]
    await r.router.navigate('/heartbeats/h1')
    await screen.findByText('Agendamento')
    expect(screen.queryByText(/prazo/i)).not.toBeInTheDocument()
  })

  it('heartbeat que não existe mostra o erro da rota, sem quebrar', async () => {
    const r = await renderizarPainel('/')
    await r.router.navigate('/heartbeats/nada')
    expect(await screen.findByRole('alert')).toHaveTextContent('Não encontrado.')
  })
})
