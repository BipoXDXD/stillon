import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { eventoDeHeartbeat, heartbeat, incidente, incidenteDeHeartbeat } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { tempoDeExecucao } from './estado'

type ComListas = {
  eventos?: Parameters<typeof eventoDeHeartbeat>[0][]
  incidentes?: Parameters<typeof incidenteDeHeartbeat>[0][]
}

async function detalheCom(p: ComListas & Parameters<typeof heartbeat>[0] = {}) {
  const r = await renderizarPainel('/')
  const { eventos: _eventos, incidentes: _incidentes, ...doHeartbeat } = p
  r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', ...doHeartbeat })]
  r.falsa.estado.eventos = (p.eventos ?? []).map(eventoDeHeartbeat)
  r.falsa.estado.incidentesDeHeartbeat = (p.incidentes ?? []).map((i) => incidenteDeHeartbeat({ heartbeat_id: 'h1', ...i }))
  await r.router.navigate('/heartbeats/h1')
  await screen.findByRole('heading', { level: 1, name: 'Backup' })
  return r
}

const secao = (nome: string) => screen.findByRole('region', { name: nome })

describe('tempoDeExecucao', () => {
  it.each([
    [0, '0 ms'],
    [999, '999 ms'],
    [1000, '1 s'],
    [59_400, '59 s'],
    [60_000, '1 min'],
    [3_780_000, '1 h 3 min'],
  ])('%i ms → %s', (ms, texto) => {
    expect(tempoDeExecucao(ms)).toBe(texto)
  })
})

describe('descrição no detalhe', () => {
  // Sabotagem: tirar o whitespace-pre-wrap → as linhas viram uma só na tela.
  it('mostra o texto com as quebras de linha', async () => {
    await detalheCom({ description: 'Roda no NAS.\nSe falhar, ver o log.' })
    const p = screen.getByText(/Roda no NAS/)
    expect(p.textContent).toBe('Roda no NAS.\nSe falhar, ver o log.')
    expect(p).toHaveClass('whitespace-pre-wrap')
  })

  // HTML no texto é texto: quem escreveu é o dono, mas nada ali vira marcação.
  it('marcação na descrição aparece como texto', async () => {
    await detalheCom({ description: '<img src=x onerror=alert(1)>' })
    expect(screen.getByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
    expect(document.querySelector('img[src="x"]')).toBeNull()
  })

  it('sem descrição, não sobra parágrafo vazio', async () => {
    await detalheCom()
    expect(document.querySelector('p.whitespace-pre-wrap')).toBeNull()
  })
})

describe('tempo de execução no detalhe', () => {
  // Zero é medida: um `||` no lugar do `=== null` diria "Não medida".
  it('mostra a última duração, inclusive a de zero ms', async () => {
    await detalheCom({ last_runtime_ms: 0 })
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText('Última duração').nextElementSibling).toHaveTextContent('0 ms')
  })

  it('sem medida, diz isso, e não fala em execução parada', async () => {
    await detalheCom()
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText('Última duração').nextElementSibling).toHaveTextContent('Não medida')
    expect(within(s).queryByText('Em execução desde')).not.toBeInTheDocument()
  })

  it('o detalhe mostra as tags', async () => {
    await detalheCom({ tags: ['prod', 'dados'] })
    expect(within(screen.getByRole('list', { name: 'Tags' })).getAllByRole('listitem').map((l) => l.textContent)).toEqual(['prod', 'dados'])
  })

  // A mesma regra da lista: inicioEmCurso nas duas telas.
  it.each(['paused', 'late', 'failing'] as const)('%s com início aberto não fala em execução', async (state) => {
    await detalheCom({ state, enabled: state !== 'paused', last_start_at: '2026-09-21T11:50:00Z' })
    const s = await secao('Agendamento e sinais')
    expect(within(s).queryByText('Em execução desde')).not.toBeInTheDocument()
  })

  it('tarefa nova com início: aguarda o primeiro sucesso e mostra a execução', async () => {
    await detalheCom({ state: 'waiting', last_ping_at: '2026-09-24T11:50:00Z', last_start_at: '2026-09-24T11:50:00Z', last_success_at: null })
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText(/^Aguardando o primeiro sucesso, até /)).toBeInTheDocument()
    expect(within(s).getByText('Em execução desde')).toBeInTheDocument()
  })

  it('com início aberto, mostra desde quando', async () => {
    await detalheCom({ last_start_at: '2026-09-24T11:50:00Z' })
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText('Em execução desde')).toBeInTheDocument()
  })
})

describe('log de eventos', () => {
  it('sem sinal, diz isso', async () => {
    await detalheCom()
    expect(await within(await secao('Log de eventos')).findByText('Nenhum sinal recebido.')).toBeInTheDocument()
  })

  // Sabotagem: trocar o texto de fail → cor sozinha não diz o tipo.
  it('mostra o tipo em texto, a hora e o tempo de execução quando há', async () => {
    await detalheCom({
      eventos: [
        { id: 'e1', kind: 'success', runtime_ms: 183_000 },
        { id: 'e2', kind: 'start' },
        { id: 'e3', kind: 'fail' },
      ],
    })
    const s = await secao('Log de eventos')
    const linhas = await within(s).findAllByRole('listitem')
    expect(linhas.map((l) => within(l).getByText(/^(Início|Sucesso|Falha)$/).textContent)).toEqual(['Sucesso', 'Início', 'Falha'])
    expect(linhas[0]).toHaveTextContent('levou 3 min')
    expect(linhas[1]).not.toHaveTextContent('levou')
    expect(linhas[0]).not.toHaveTextContent('código de saída')
  })

  // Zero é um código de verdade: um `&&` no lugar do `!== null` o esconderia.
  it('mostra o código de saída quando a tarefa mandou, inclusive o zero', async () => {
    await detalheCom({
      eventos: [
        { id: 'e1', kind: 'fail', exit_code: 3 },
        { id: 'e2', kind: 'success', exit_code: 0 },
      ],
    })
    const linhas = await within(await secao('Log de eventos')).findAllByRole('listitem')
    expect(linhas[0]).toHaveTextContent('código de saída 3')
    expect(linhas[1]).toHaveTextContent('código de saída 0')
  })

  // Sabotagem: não passar o cursor → a mesma página duas vezes.
  it('carrega mais pelo cursor, e o foco vai ao título quando o botão some', async () => {
    const eventos = Array.from({ length: 20 }, (_, i) => ({ id: `e${i}`, kind: 'success' as const }))
    const r = await detalheCom({ eventos: [...eventos, { id: 'e20', kind: 'fail' }] })
    const s = await secao('Log de eventos')
    expect(await within(s).findAllByRole('listitem')).toHaveLength(20)
    await r.usuario.click(within(s).getByRole('button', { name: 'Carregar mais' }))
    await waitFor(() => expect(within(s).getAllByRole('listitem')).toHaveLength(21))
    expect(within(s).queryByRole('button', { name: 'Carregar mais' })).not.toBeInTheDocument()
    expect(within(s).getByRole('heading', { name: 'Log de eventos' })).toHaveFocus()
  })

  it('erro do log não derruba o detalhe', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup' })]
    r.falsa.estado.forcar.set('GET /heartbeats/:id/events', { status: 500, code: 'internal_error' })
    await r.router.navigate('/heartbeats/h1')
    expect(await within(await secao('Log de eventos')).findByRole('alert')).toHaveTextContent('Erro interno do servidor.')
    expect(screen.getByRole('heading', { level: 1, name: 'Backup' })).toBeInTheDocument()
  })
})

describe('incidentes do heartbeat', () => {
  it('sem incidente, diz isso', async () => {
    await detalheCom()
    expect(await within(await secao('Incidentes')).findByText('Nenhum incidente.')).toBeInTheDocument()
  })

  it('mostra a causa em português, a situação e o período', async () => {
    await detalheCom({
      incidentes: [
        { id: 'i1', cause: 'heartbeat_late', started_at: '2026-09-24T11:00:00Z' },
        { id: 'i2', cause: 'heartbeat_failed', started_at: '2026-09-23T11:00:00Z', resolved_at: '2026-09-23T11:30:00Z' },
      ],
    })
    const s = await secao('Incidentes')
    const [aberto, resolvido] = await within(s).findAllByRole('listitem')
    expect(aberto).toHaveTextContent('Em andamento')
    expect(aberto).toHaveTextContent('Sem sinal no prazo')
    expect(resolvido).toHaveTextContent('Resolvido')
    expect(resolvido).toHaveTextContent('A tarefa avisou falha')
    expect(resolvido).toHaveTextContent('durou 30 min')
  })

  // Sabotagem: chamar o ack de /incidents → 404 na API falsa, e o botão fica.
  it('reconhecer vai à rota do heartbeat, some o botão e o foco fica na seção', async () => {
    const r = await detalheCom({ incidentes: [{ id: 'i1' }] })
    const s = await secao('Incidentes')
    await r.usuario.click(await within(s).findByRole('button', { name: /^Reconhecer o incidente/ }))
    await within(s).findByText(/lembretes parados/)
    expect(r.falsa.chamadas('POST /heartbeats/:id/incidents/:incident/ack')).toBe(1)
    expect(within(s).queryByRole('button', { name: /^Reconhecer/ })).not.toBeInTheDocument()
    expect(within(s).getByRole('heading', { name: 'Incidentes' })).toHaveFocus()
  })

  it('incidente resolvido não oferece Reconhecer', async () => {
    await detalheCom({ incidentes: [{ id: 'i1', resolved_at: '2026-09-24T11:30:00Z' }] })
    const s = await secao('Incidentes')
    await within(s).findByText('Resolvido')
    expect(within(s).queryByRole('button', { name: /^Reconhecer/ })).not.toBeInTheDocument()
  })

  it('erro ao reconhecer aparece na tela, e o botão fica', async () => {
    const r = await detalheCom({ incidentes: [{ id: 'i1' }] })
    r.falsa.estado.forcar.set('POST /heartbeats/:id/incidents/:incident/ack', { status: 500, code: 'internal_error' })
    const s = await secao('Incidentes')
    await r.usuario.click(await within(s).findByRole('button', { name: /^Reconhecer o incidente/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Erro interno do servidor.')
    expect(within(s).getByRole('button', { name: /^Reconhecer o incidente/ })).toBeInTheDocument()
  })

  it('heartbeat apagado em outra aba: reconhecer volta ao painel', async () => {
    const r = await detalheCom({ incidentes: [{ id: 'i1' }] })
    const s = await secao('Incidentes')
    const botao = await within(s).findByRole('button', { name: /^Reconhecer o incidente/ })
    r.falsa.estado.incidentesDeHeartbeat = []
    await r.usuario.click(botao)
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
  })
})

describe('tela de incidentes e contador', () => {
  const atrasado = heartbeat({ id: 'h1', name: 'Backup', state: 'late', open_incident_id: 'i1' })

  // Spec da tela, T13. late e failing são derivados na leitura, e quem abre o
  // incidente é o avaliador: com ele parado, não há incidente nenhum.
  // Sabotagem: olhar só o open_incident_id → os dois primeiros casos ficam verdes.
  it.each([
    ['atrasada, sem incidente', { state: 'late', open_incident_id: null }, 'Atrasado'],
    ['falhando, sem incidente', { state: 'failing', open_incident_id: null }, 'Falhando'],
    ['atrasada, com incidente', { state: 'late', open_incident_id: 'i1' }, 'Atrasado'],
    ['pausada, com incidente ainda aberto', { state: 'paused', enabled: false, open_incident_id: 'i1' }, 'Pausado'],
  ] as const)('aponta a tarefa %s, sem o vazio verde', async (_, p, selo) => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', ...p })]
    await r.cliente.refetchQueries()
    const s = await screen.findByRole('region', { name: 'Tarefas que pedem atenção' })
    expect(within(s).getByRole('link', { name: 'Backup' })).toHaveAttribute('href', '/heartbeats/h1')
    expect(within(s).getByText(selo)).toBeInTheDocument()
    expect(await screen.findByText('Nenhum alvo com incidente aberto.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum incidente aberto.')).not.toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Incidentes, 1 aberto' })).toBeInTheDocument()
  })

  // Sabotagem: Vazio só com soDosAlvos no filtro open.
  it('na aba Todos, com tarefa fora, "Nenhum incidente." não nega a seção de cima', async () => {
    const r = await renderizarPainel('/incidents?status=all')
    r.falsa.estado.heartbeats = [atrasado]
    await r.cliente.refetchQueries()
    await screen.findByRole('region', { name: 'Tarefas que pedem atenção' })
    expect(await screen.findByText('Nenhum alvo com incidente.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum incidente.')).not.toBeInTheDocument()
  })

  it('na aba Todos, sem resposta dos heartbeats, também não', async () => {
    const r = await renderizarPainel('/incidents?status=all')
    r.falsa.estado.forcar.set('GET /heartbeats', { status: 500, code: 'internal_error' })
    await r.cliente.resetQueries()
    expect(await screen.findByText('Nenhum alvo com incidente.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum incidente.')).not.toBeInTheDocument()
  })

  it('na aba Todos, sem tarefa fora, a frase de sempre', async () => {
    const r = await renderizarPainel('/incidents?status=all')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h2', name: 'Outro' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Nenhum incidente.')).toBeInTheDocument()
  })

  it('sem resposta dos heartbeats, o vazio verde também não aparece', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.forcar.set('GET /heartbeats', { status: 500, code: 'internal_error' })
    await r.cliente.resetQueries()
    expect(await screen.findByText('Nenhum alvo com incidente aberto.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum incidente aberto.')).not.toBeInTheDocument()
  })

  it('sem tarefa em incidente, a tela é a de sempre', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h2', name: 'Outro' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Nenhum incidente aberto.')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Tarefas que pedem atenção' })).not.toBeInTheDocument()
  })

  // Sabotagem: contar só /incidents → a aba não diz nada.
  it('o contador da aba soma alvos e heartbeats', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.incidentes = [incidente({ id: 'a1' })]
    r.falsa.estado.heartbeats = [atrasado]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('link', { name: 'Incidentes, 2 abertos' })).toBeInTheDocument()
  })
})

// Dados de uma versão posterior do servidor (SP1, piso de rollback): o painel
// antigo mostra a linha, sem quebrar o parse e sem oferecer edição.
describe('heartbeat de versão posterior', () => {
  // Sabotagem: tirar o ramo unknown do Agendamento → o detalhe não carrega.
  it('agendamento desconhecido: diz que não é suportado e não oferece Editar', async () => {
    await detalheCom({ schedule: { kind: 'unknown' } })
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText('Agendamento').nextElementSibling).toHaveTextContent('Agendamento não suportado nesta versão')
    expect(screen.queryByRole('link', { name: 'Editar' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Apagar' })).toBeInTheDocument()
  })

  it('agendamento conhecido continua com Editar', async () => {
    await detalheCom()
    expect(screen.getByRole('link', { name: 'Editar' })).toHaveAttribute('href', '/heartbeats/h1/edit')
  })

  it('a rota de edição de um agendamento desconhecido não monta o formulário', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', schedule: { kind: 'unknown' } })]
    await r.router.navigate('/heartbeats/h1/edit')
    expect(await screen.findByText('Agendamento não suportado nesta versão.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Salvar' })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Voltar ao heartbeat' })).toHaveAttribute('href', '/heartbeats/h1')
  })

  // Sabotagem: tirar o 'unknown' de EventoDeHeartbeat.kind → o log inteiro vira erro.
  it('evento de tipo novo aparece como "Outro sinal" e não derruba o log', async () => {
    await detalheCom({
      eventos: [
        { id: 'e1', kind: 'log' },
        { id: 'e2', kind: 'success' },
      ],
    })
    const linhas = await within(await secao('Log de eventos')).findAllByRole('listitem')
    expect(linhas.map((l) => within(l).getByText(/^(Outro sinal|Sucesso)$/).textContent)).toEqual(['Outro sinal', 'Sucesso'])
  })

  // Sabotagem: tirar o 'unknown' de EstadoDoHeartbeat → o detalhe vira erro.
  it('estado novo aparece como "Estado desconhecido", sem palavra de alerta', async () => {
    await detalheCom({ state: 'grace' })
    expect(screen.getByText('Estado desconhecido')).toBeInTheDocument()
    expect(screen.queryByText(/Atrasado|Falhando|Fora do ar/)).not.toBeInTheDocument()
  })

  // O PATCH numa linha de agendamento desconhecido é 409 schedule_unsupported.
  it('pausar recusado com schedule_unsupported mostra a mensagem do código', async () => {
    const r = await detalheCom({ schedule: { kind: 'unknown' } })
    r.falsa.estado.forcar.set('PATCH /heartbeats/:id', { status: 409, code: 'schedule_unsupported' })
    await r.usuario.click(screen.getByRole('button', { name: 'Pausar' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar heartbeat' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Este agendamento não pode ser editado nesta versão.')
  })
})
