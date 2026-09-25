import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { alvo, incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

// Uma resposta segurada até o teste soltar.
function portao() {
  let soltar!: () => void
  const aberto = new Promise<void>((r) => (soltar = r))
  return { aberto, soltar }
}

describe('detalhe', () => {
  it('resumo em texto, tabela, incidentes do alvo pelo filtro target_id', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'down' })]
    r.falsa.estado.serie = {
      resolution: 'raw',
      items: [
        { checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null },
        { checked_at: '2026-09-24T11:01:00Z', status: 'down', latency_ms: 10000, status_code: null, error_kind: 'timeout' },
      ],
    }
    r.falsa.estado.incidentes = [incidente(), incidente({ id: 'inc-2', target_id: 'outro', target_name: 'Outro' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { name: 'API' })).toBeInTheDocument()
    expect(await screen.findByText(/Mediana 100 ms, p95 100 ms, 50,00% no ar nesta janela/)).toBeInTheDocument()
    expect(screen.getByText('Resolução por check.', { exact: false })).toBeInTheDocument()
    const tabela = screen.getByRole('table')
    expect(within(tabela).getByText('Tempo esgotado')).toBeInTheDocument()
    // Só o incidente deste alvo: a lista vem de /incidents?target_id=.
    const secao = screen.getByRole('region', { name: 'Incidentes recentes' })
    await waitFor(() => expect(within(secao).getAllByRole('listitem')).toHaveLength(1))
  })

  // B6 do Plano 10: a série pede a janela pelo nome (?window=1h), e o
  // servidor calcula o from com o relógio dele. Antes, o from saía do
  // relógio do navegador: adiantado 1 h, a janela "1 h" pedia um from
  // depois do "agora" do servidor, e a série respondia 400. Sabotagem:
  // mandar também o from no serie() do cliente → 400 da API falsa.
  it('a série pede a janela ao servidor pelo nome, sem from do navegador', async () => {
    const r = await renderizarPainel('/targets/alvo-1?janela=1h')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = {
      resolution: 'raw',
      items: [{ checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null }],
    }
    await r.cliente.refetchQueries()
    expect(await screen.findByText(/Mediana 100 ms/)).toBeInTheDocument()
    const consultas = () => r.falsa.corpos('GET /targets/:id/checks')
    expect(consultas()[consultas().length - 1]).toEqual({ window: '1h' })
    await r.usuario.click(screen.getByRole('button', { name: '30 dias' }))
    await waitFor(() => expect(consultas()[consultas().length - 1]).toEqual({ window: '30d' }))
  })

  it('a janela acima de 48h desenha pelo que o servidor decidiu (resolution 1h)', async () => {
    const r = await renderizarPainel('/targets/alvo-1?janela=7d')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = {
      resolution: '1h',
      items: [{ bucket: '2026-09-24T10:00:00Z', total: 60, ok: 60, p50_ms: 120, p95_ms: 300, p99_ms: 400, max_ms: 500 }],
    }
    await r.cliente.refetchQueries()
    expect(await screen.findByText(/Resolução por hora/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '7 dias' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('apagar pede confirmação e volta ao painel', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    const dialogo = await screen.findByRole('alertdialog')
    expect(within(dialogo).getByText(/Não há como desfazer/)).toBeInTheDocument()
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Apagar alvo' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    expect(r.falsa.chamadas('DELETE /targets/:id')).toBe(1)
  })

  it('Cancelar e Esc não apagam', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const fechado = () => waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancelar' }))
    await fechado()
    // Uma volta à API depois: um DELETE disparado no fechar já teria chegado.
    await r.cliente.refetchQueries()
    expect(r.falsa.chamadas('DELETE /targets/:id')).toBe(0)

    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    await screen.findByRole('alertdialog')
    await r.usuario.keyboard('{Escape}')
    await fechado()
    await r.cliente.refetchQueries()
    expect(r.falsa.chamadas('DELETE /targets/:id')).toBe(0)
    expect(r.router.state.location.pathname).toBe('/targets/alvo-1')
  })

  // Revisão da Task 6: sem Trigger, o Radix devolvia o foco ao <body>.
  it('o foco começa em Cancelar e volta ao Apagar ao fechar', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const apagar = await screen.findByRole('button', { name: 'Apagar' })

    await r.usuario.click(apagar)
    const cancelar = within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancelar' })
    await waitFor(() => expect(cancelar).toHaveFocus())
    await r.usuario.click(cancelar)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(apagar).toHaveFocus())

    await r.usuario.click(apagar)
    await screen.findByRole('alertdialog')
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(apagar).toHaveFocus())
  })

  it('com a exclusão em curso, Cancelar fica desabilitado e Esc não fecha', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const p = portao()
    servidor.use(
      http.delete('*/api/v1/targets/:id', async () => {
        await p.aberto
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    const dialogo = await screen.findByRole('alertdialog')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Apagar alvo' }))
    await waitFor(() => expect(within(dialogo).getByRole('button', { name: 'Cancelar' })).toBeDisabled())
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    p.soltar()
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
  })

  it('apagar não refaz o GET do alvo apagado, e atualiza os incidentes', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar' }))
    const dialogo = await screen.findByRole('alertdialog')
    const getsDoAlvo = r.falsa.chamadas('GET /targets/:id')
    const getsDeIncidentes = r.falsa.chamadas('GET /incidents')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Apagar alvo' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    await waitFor(() => expect(r.falsa.chamadas('GET /incidents')).toBeGreaterThan(getsDeIncidentes))
    expect(r.falsa.chamadas('GET /targets/:id')).toBe(getsDoAlvo)
    expect(r.cliente.getQueryCache().findAll({ queryKey: ['targets', 'alvo-1'] })).toHaveLength(0)
  })

  it('Pausar e Retomar mandam o PATCH com enabled', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Retomar' }))
    await screen.findByRole('button', { name: 'Pausar' })
    expect(r.falsa.estado.corpos.get('PATCH /targets/:id')).toEqual([{ enabled: false }, { enabled: true }])
  })

  // D17: um poll de fundo que falha não apaga a lista de incidentes do alvo.
  it('um poll de incidentes que falha mantém a lista e avisa que ela está velha', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    const secao = await screen.findByRole('region', { name: 'Incidentes recentes' })
    await waitFor(() => expect(within(secao).getAllByRole('listitem')).toHaveLength(1))
    r.falsa.estado.forcar.set('GET /incidents', { status: 500, code: 'internal' })
    await r.cliente.refetchQueries({ queryKey: ['incidents'] })
    expect(await within(secao).findByText(/Não foi possível atualizar agora/)).toBeInTheDocument()
    expect(within(secao).getAllByRole('listitem')).toHaveLength(1)
  })

  it('trocando de janela, o bloco fica ocupado até a janela nova chegar', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = {
      resolution: 'raw',
      items: [{ checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null }],
    }
    await r.cliente.refetchQueries()
    const resumo = await screen.findByText(/Mediana 100 ms/)
    const bloco = resumo.closest('[aria-busy]')
    expect(bloco).toHaveAttribute('aria-busy', 'false')
    const p = portao()
    servidor.use(
      http.get('*/api/v1/targets/:id/checks', async () => {
        await p.aberto
        return HttpResponse.json({
          resolution: '1h',
          items: [{ bucket: '2026-09-24T10:00:00Z', total: 60, ok: 60, p50_ms: 120, p95_ms: 300, p99_ms: 400, max_ms: 500 }],
        })
      }),
    )
    await r.usuario.click(screen.getByRole('button', { name: '7 dias' }))
    const latencia = screen.getByRole('region', { name: 'Latência' })
    await waitFor(() => expect(within(latencia).getByText('Carregando…')).toBeInTheDocument())
    expect(within(latencia).getByText(/Mediana 100 ms/).closest('[aria-busy]')).toHaveAttribute('aria-busy', 'true')
    p.soltar()
    expect(await within(latencia).findByText(/Resolução por hora/)).toBeInTheDocument()
    expect(within(latencia).queryByText('Carregando…')).not.toBeInTheDocument()
    expect(within(latencia).getByText(/Mediana das horas 120 ms/).closest('[aria-busy]')).toHaveAttribute(
      'aria-busy',
      'false',
    )
  })

  // O nome e a descrição do <svg> do gráfico estão em grafico.test.tsx: aqui
  // o jsdom não mede o ResponsiveContainer, e o svg nem é desenhado.
  it('o gráfico e o resumo que o descreve estão na tela; a tabela diz que são os 200 mais recentes', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = {
      resolution: 'raw',
      items: [{ checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null }],
    }
    await r.cliente.refetchQueries()
    expect(await screen.findByTestId('grafico-latencia')).toBeInTheDocument()
    expect(screen.getByText(/Mediana 100 ms, p95 100 ms/)).toHaveAttribute('id')
    expect(screen.getByRole('table', { name: /200 checks mais recentes/ })).toBeInTheDocument()
    expect(screen.getByText(/Ver dados em tabela \(até os 200 mais recentes\)/)).toBeInTheDocument()
  })

  // Revisão final: de um detalhe a outro, a tela é a mesma rota, e sem
  // key={id} o erro, o diálogo e o "ocupado" de A seguiam em B, e o título
  // de A (no cache) não recebia o foco ao voltar. Sabotagem: devolver a
  // rota a <DetalheDoAlvo /> sem key → o erro de A aparece em B.
  it('de um alvo a outro, a tela recomeça: sem o erro do anterior, com o foco no título', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo({ id: 'alvo-1', name: 'Site A' }), alvo({ id: 'alvo-2', name: 'Site B' })]
    await r.cliente.refetchQueries()
    r.falsa.estado.forcar.set('PATCH /targets/:id', { status: 500, code: 'internal' })
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    await r.router.navigate('/targets/alvo-2')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Site B', level: 1 })).toHaveFocus())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    // O foco sai do <h1> (que, reaproveitado, o manteria à toa).
    act(() => screen.getByRole('button', { name: 'Pausar' }).focus())
    await r.router.navigate(-1)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Site A', level: 1 })).toHaveFocus())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('janela desconhecida na URL cai nas 24 h, sem quebrar', async () => {
    const r = await renderizarPainel('/targets/alvo-1?janela=toString')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('button', { name: '24 h' })).toHaveAttribute('aria-pressed', 'true')
  })

})

// M3 da revisão final: 404 tratado como em Canais. O alvo sumiu (apagado em
// outra aba): o painel explica, tira o alvo do cache e volta à lista.
// Sabotagens: em DetalheDoAlvo, trocar o "if (naoEncontrado(e))" de
// alternar() e de apagar() por "if (naoEncontrado(e) && Math.random() > 2)"
// (o erro cru fica na tela); e no efeito do poll, tirar a chamada a sumiu().
describe('o alvo sumiu', () => {
  const TEXTO = 'O alvo já tinha sido apagado, talvez em outra aba.'

  async function aberto() {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await screen.findByRole('heading', { name: 'API', level: 1 })
    return r
  }

  it('Pausar com 404 explica e volta à lista', async () => {
    const r = await aberto()
    r.falsa.estado.alvos = []
    await r.usuario.click(screen.getByRole('button', { name: 'Pausar' }))
    expect((await screen.findAllByText(TEXTO))[0]).toBeInTheDocument()
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    expect(r.cliente.getQueryCache().findAll({ queryKey: ['targets', 'alvo-1'] })).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('Apagar com 404 explica e volta à lista', async () => {
    const r = await aberto()
    r.falsa.estado.alvos = []
    await r.usuario.click(screen.getByRole('button', { name: 'Apagar' }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Apagar alvo' }))
    expect((await screen.findAllByText(TEXTO))[0]).toBeInTheDocument()
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('o poll que volta 404 explica e volta à lista, sem "dados velhos"', async () => {
    const r = await aberto()
    r.falsa.estado.alvos = []
    await r.cliente.refetchQueries({ queryKey: ['targets', 'alvo-1'], exact: true })
    expect((await screen.findAllByText(TEXTO))[0]).toBeInTheDocument()
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    expect(screen.queryByText(/Não foi possível atualizar agora/)).not.toBeInTheDocument()
  })

  it('um endereço de alvo que nunca existiu mostra "Não encontrado", sem sair da tela', async () => {
    const r = await renderizarPainel('/targets/nao-existe')
    expect(await screen.findByRole('alert')).toHaveTextContent('Não encontrado.')
    expect(r.router.state.location.pathname).toBe('/targets/nao-existe')
  })
})

