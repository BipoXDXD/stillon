import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { PING_URL } from '@/test/api-falsa'
import { eventoDeHeartbeat, heartbeat, incidente, incidenteDeHeartbeat } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'
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

  // Sabotagem: deixar log como "Outro sinal", ou tirar a origem e a execução da linha.
  it('o registro tem selo próprio, e a linha diz a execução e o que veio do painel', async () => {
    await detalheCom({
      eventos: [
        { id: 'e1', kind: 'log', run_id: '0b3f6c2e-9a1d-4e7f-8c55-1234567890ab' },
        { id: 'e2', kind: 'success', source: 'panel' },
      ],
    })
    const linhas = await within(await secao('Log de eventos')).findAllByRole('listitem')
    expect(linhas.map((l) => within(l).getByText(/^(Registro|Sucesso)$/).textContent)).toEqual(['Registro', 'Sucesso'])
    const [registro, sucesso] = linhas
    if (registro === undefined || sucesso === undefined) throw new Error('o log não tem as duas linhas')
    expect(within(registro).getByText('execução 0b3f6c2e')).toBeInTheDocument()
    expect(registro).not.toHaveTextContent('pelo painel')
    expect(within(sucesso).getByText('pelo painel')).toBeInTheDocument()
    expect(sucesso).not.toHaveTextContent('execução')
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

describe('em tolerância no detalhe', () => {
  // Sabotagens: tirar o ramo grace do fraseDoEstado → a frase some; tirar o
  // ramo grace do Prazo → "Próximo prazo" com a hora do alarme.
  it('mostra o selo, a hora do alarme e nenhuma palavra de alerta', async () => {
    await detalheCom({ state: 'grace', deadline_at: '2099-01-01T12:00:00Z' })
    expect(screen.getByText('Em tolerância')).toBeInTheDocument()
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText(/^Sem sinal desde o prazo esperado; o alarme sai às 01\/01\/2099,? \d{2}:\d{2}\.$/)).toBeInTheDocument()
    expect(within(s).getByText('Alarme').nextElementSibling).toHaveTextContent(/^01\/01\/2099,? \d{2}:\d{2}$/)
    expect(within(s).queryByText(/Próximo prazo|Prazo vencido/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Atrasado|Falhando|Fora do ar|Estado desconhecido/)).not.toBeInTheDocument()
  })
})

// Segura uma rota da API falsa até soltar: o que a tela mostra no intervalo.
function portao() {
  let soltar = () => {}
  const aberto = new Promise<void>((resolve) => {
    soltar = resolve
  })
  return { aberto, soltar }
}

describe('sinalizar agora', () => {
  const AVISO = 'Sinal de sucesso registrado.'
  const avisou = async () => expect((await screen.findAllByText(AVISO)).length).toBeGreaterThan(0)

  // Sabotagem: não invalidar os eventos → o log fica em "Nenhum sinal recebido.".
  it('chama a rota, avisa e o log mostra o sinal do painel', async () => {
    const r = await detalheCom()
    const s = await secao('Log de eventos')
    await within(s).findByText('Nenhum sinal recebido.')
    await r.usuario.click(screen.getByRole('button', { name: 'Sinalizar agora' }))
    await avisou()
    expect(r.falsa.chamadas('POST /heartbeats/:id/ping-now')).toBe(1)
    const linha = await within(s).findByRole('listitem')
    expect(within(linha).getByText('Sucesso')).toBeInTheDocument()
    expect(within(linha).getByText('pelo painel')).toBeInTheDocument()
  })

  // Atraso só por prazo: a rota devolve "Em dia", e entre o clique e o refetch
  // a tela não volta a dizer "Atrasado". Sabotagem: tirar o setQueryData.
  it('atraso por prazo: mostra o estado devolvido pela rota antes do refetch', async () => {
    const r = await detalheCom({ state: 'late', open_incident_id: null, deadline_at: '2026-09-24T11:00:00Z' })
    expect(screen.getByText('Atrasado')).toBeInTheDocument()
    const p = portao()
    servidor.use(
      http.get('*/api/v1/heartbeats/h1', async () => {
        await p.aberto
        return undefined
      }),
    )
    await r.usuario.click(screen.getByRole('button', { name: 'Sinalizar agora' }))
    await avisou()
    expect(screen.queryByText('Atrasado')).not.toBeInTheDocument()
    expect(screen.getByText('Em dia')).toBeInTheDocument()
    p.soltar()
    await waitFor(() => expect(r.cliente.isFetching()).toBe(0))
    expect(screen.queryByText('Atrasado')).not.toBeInTheDocument()
  })

  // O sinal não fecha o incidente (statusOf do servidor: incidente aberto é
  // late). Sabotagem: a API falsa zerar o open_incident_id → "Em dia".
  it('com incidente aberto, o sinal registra e a tarefa continua Atrasado', async () => {
    const r = await detalheCom({ state: 'late', open_incident_id: 'i1', deadline_at: '2026-09-24T11:00:00Z' })
    await r.usuario.click(screen.getByRole('button', { name: 'Sinalizar agora' }))
    await avisou()
    await waitFor(() => expect(r.cliente.isFetching()).toBe(0))
    expect(screen.getByText('Atrasado')).toBeInTheDocument()
    expect(screen.queryByText('Em dia')).not.toBeInTheDocument()
  })

  // Sabotagem: tirar a guarda do ocupado → dois sinais.
  it('fica ocupado enquanto a rota responde, sem duplo clique', async () => {
    const r = await detalheCom()
    const p = portao()
    servidor.use(
      http.post('*/api/v1/heartbeats/h1/ping-now', async () => {
        await p.aberto
        return undefined
      }),
    )
    const botao = screen.getByRole('button', { name: 'Sinalizar agora' })
    await r.usuario.click(botao)
    expect(botao).toHaveAttribute('aria-disabled', 'true')
    await r.usuario.click(botao)
    p.soltar()
    await avisou()
    expect(r.falsa.chamadas('POST /heartbeats/:id/ping-now')).toBe(1)
    await waitFor(() => expect(botao).not.toHaveAttribute('aria-disabled'))
  })

  it('erro fica na tela, e o heartbeat também', async () => {
    const r = await detalheCom()
    r.falsa.estado.forcar.set('POST /heartbeats/:id/ping-now', { status: 500, code: 'internal_error' })
    await r.usuario.click(screen.getByRole('button', { name: 'Sinalizar agora' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Erro interno do servidor.')
    expect(screen.queryByText(AVISO)).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'Backup' })).toBeInTheDocument()
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

  // O clone de um agendamento desconhecido é 409 schedule_unsupported.
  // Sabotagem: o botão fora da condição.
  it('agendamento desconhecido não oferece Duplicar', async () => {
    await detalheCom({ schedule: { kind: 'unknown' } })
    expect(screen.queryByRole('button', { name: 'Duplicar' })).not.toBeInTheDocument()
  })

  it('agendamento conhecido oferece Duplicar', async () => {
    await detalheCom()
    expect(screen.getByRole('button', { name: 'Duplicar' })).toBeInTheDocument()
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
        { id: 'e1', kind: 'futuro' },
        { id: 'e2', kind: 'success' },
      ],
    })
    const linhas = await within(await secao('Log de eventos')).findAllByRole('listitem')
    expect(linhas.map((l) => within(l).getByText(/^(Outro sinal|Sucesso)$/).textContent)).toEqual(['Outro sinal', 'Sucesso'])
  })

  // Sabotagem: tirar o 'unknown' de EstadoDoHeartbeat → o detalhe vira erro.
  it('estado novo aparece como "Estado desconhecido", sem palavra de alerta', async () => {
    await detalheCom({ state: 'futuro' })
    expect(screen.getByText('Estado desconhecido')).toBeInTheDocument()
    expect(screen.queryByText(/Atrasado|Falhando|Fora do ar/)).not.toBeInTheDocument()
  })

  // Todo PATCH nessa linha é 409 schedule_unsupported: pausar e retomar
  // falhariam sempre. Sabotagem: o botão fora da condição.
  it.each([
    ['ativo', { enabled: true, state: 'up' }],
    ['pausado', { enabled: false, state: 'paused' }],
  ] as const)('agendamento desconhecido %s não oferece Pausar nem Retomar', async (_, p) => {
    await detalheCom({ schedule: { kind: 'unknown' }, ...p })
    expect(screen.queryByRole('button', { name: 'Pausar' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retomar' })).not.toBeInTheDocument()
  })

  it('agendamento desconhecido ainda pode ser apagado', async () => {
    const r = await detalheCom({ schedule: { kind: 'unknown' } })
    await r.usuario.click(screen.getByRole('button', { name: 'Apagar' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar heartbeat' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    expect(r.falsa.chamadas('DELETE /heartbeats/:id')).toBe(1)
    expect(r.falsa.estado.heartbeats).toEqual([])
  })

  // O servidor não move o prazo dessa linha: a data guardada mentiria.
  // Sabotagem: tirar o ramo unknown do Prazo → "Próximo prazo" com a data.
  it('agendamento desconhecido não afirma prazo', async () => {
    await detalheCom({ schedule: { kind: 'unknown' }, deadline_at: '2099-01-01T12:00:00Z' })
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText('Prazo').nextElementSibling).toHaveTextContent('Prazo não acompanhado nesta versão')
    expect(within(s).queryByText(/Próximo prazo|Prazo vencido/)).not.toBeInTheDocument()
  })

  // Sabotagem: tirar o ramo unknown do fraseDoEstado → ", até <data>" volta.
  it('agendamento desconhecido esperando não promete data', async () => {
    await detalheCom({ schedule: { kind: 'unknown' }, state: 'waiting', last_ping_at: null, deadline_at: '2099-01-01T12:00:00Z' })
    const s = await secao('Agendamento e sinais')
    expect(within(s).getByText('Aguardando o primeiro sinal.')).toBeInTheDocument()
    expect(within(s).queryByText(/Aguardando o primeiro sinal, até /)).not.toBeInTheDocument()
  })
})

describe('duplicar', () => {
  const dialogoDaUrl = () => screen.findByRole('dialog', { name: 'Guarde a URL de ping' })

  // Sabotagem: navegar antes do "Já guardei" → a URL da cópia some sem ser vista.
  it('chama a rota, mostra a URL da cópia uma vez, e "Já guardei" leva à cópia', async () => {
    const r = await detalheCom()
    await r.usuario.click(screen.getByRole('button', { name: 'Duplicar' }))
    const d = await dialogoDaUrl()
    expect(within(d).getByText(`${PING_URL}-copia`)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /heartbeats/:id/clone')).toBe(1)
    expect(r.router.state.location.pathname).toBe('/heartbeats/h1')
    await r.usuario.click(within(d).getByRole('button', { name: 'Já guardei' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/heartbeats/hb-2'))
    expect(await screen.findByRole('heading', { level: 1, name: 'Backup (cópia)' })).toBeInTheDocument()
    expect((await screen.findAllByText('Cópia criada.')).length).toBeGreaterThan(0)
    expect(screen.queryByText(`${PING_URL}-copia`)).not.toBeInTheDocument()
  })

  it('no limite de heartbeats, o aviso fica na tela e o botão volta', async () => {
    const r = await detalheCom()
    r.falsa.estado.forcar.set('POST /heartbeats/:id/clone', { status: 409, code: 'heartbeat_limit_reached' })
    await r.usuario.click(screen.getByRole('button', { name: 'Duplicar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Limite de 100 heartbeats atingido.')
    expect(screen.getByRole('button', { name: 'Duplicar' })).not.toHaveAttribute('aria-disabled')
    expect(screen.queryByRole('dialog', { name: 'Guarde a URL de ping' })).not.toBeInTheDocument()
    expect(r.router.state.location.pathname).toBe('/heartbeats/h1')
  })

  // Sabotagem: tirar o `if (ocupado) return` do executar → duas cópias.
  it('ocupado, o segundo clique não envia de novo', async () => {
    const r = await detalheCom()
    let pedidos = 0
    let soltar = () => {}
    const resposta = new Promise<void>((resolve) => {
      soltar = resolve
    })
    servidor.use(
      http.post('*/api/v1/heartbeats/h1/clone', async () => {
        pedidos += 1
        await resposta
        return HttpResponse.json(
          { ...heartbeat({ id: 'hb-9', name: 'Backup (cópia)' }), ping_url: `${PING_URL}-copia` },
          { status: 201 },
        )
      }),
    )
    const botao = screen.getByRole('button', { name: 'Duplicar' })
    await r.usuario.click(botao)
    await waitFor(() => expect(botao).toHaveAttribute('aria-disabled', 'true'))
    await r.usuario.click(botao)
    soltar()
    await dialogoDaUrl()
    expect(pedidos).toBe(1)
  })
})

describe('limpar eventos', () => {
  // Sabotagem: mostrar o botão sem olhar o log → ele aparece aqui.
  it('com o log vazio, não oferece limpar', async () => {
    await detalheCom()
    await within(await secao('Log de eventos')).findByText('Nenhum sinal recebido.')
    expect(screen.queryByRole('button', { name: 'Limpar eventos' })).not.toBeInTheDocument()
  })

  // Sabotagem: limpar sem confirmar, ou não invalidar o log depois.
  it('confirma, chama a rota, e o log fica vazio', async () => {
    const r = await detalheCom({ eventos: [{ id: 'e1', kind: 'success' }] })
    await r.usuario.click(await screen.findByRole('button', { name: 'Limpar eventos' }))
    const confirmacao = await screen.findByRole('alertdialog', { name: 'Limpar o log de eventos?' })
    expect(confirmacao).toHaveTextContent('Os sinais registrados somem. O estado, o prazo e os incidentes continuam.')
    expect(r.falsa.chamadas('POST /heartbeats/:id/events/clear')).toBe(0)
    await r.usuario.click(within(confirmacao).getByRole('button', { name: 'Limpar eventos' }))
    const s = await secao('Log de eventos')
    expect(await within(s).findByText('Nenhum sinal recebido.')).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /heartbeats/:id/events/clear')).toBe(1)
    expect((await screen.findAllByText('Eventos apagados.')).length).toBeGreaterThan(0)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Limpar eventos' })).not.toBeInTheDocument()
    // O botão sumiu com o log: o foco não cai no <body>.
    expect(screen.getByRole('heading', { level: 1, name: 'Backup' })).toHaveFocus()
  })

  it('cancelar não limpa', async () => {
    const r = await detalheCom({ eventos: [{ id: 'e1', kind: 'success' }] })
    await r.usuario.click(await screen.findByRole('button', { name: 'Limpar eventos' }))
    const confirmacao = await screen.findByRole('alertdialog', { name: 'Limpar o log de eventos?' })
    await r.usuario.click(within(confirmacao).getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(r.falsa.chamadas('POST /heartbeats/:id/events/clear')).toBe(0)
    expect(screen.getByRole('button', { name: 'Limpar eventos' })).toHaveFocus()
  })

  it('erro ao limpar aparece na tela e os eventos ficam', async () => {
    const r = await detalheCom({ eventos: [{ id: 'e1', kind: 'success' }] })
    r.falsa.estado.forcar.set('POST /heartbeats/:id/events/clear', { status: 500, code: 'internal_error' })
    await r.usuario.click(await screen.findByRole('button', { name: 'Limpar eventos' }))
    const confirmacao = await screen.findByRole('alertdialog', { name: 'Limpar o log de eventos?' })
    await r.usuario.click(within(confirmacao).getByRole('button', { name: 'Limpar eventos' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Erro interno do servidor.')
    expect(await within(await secao('Log de eventos')).findAllByRole('listitem')).toHaveLength(1)
  })
})
