import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { ItemDoResumo } from '@/lib/api/schemas'
import { horaMinuto } from '@/lib/format'
import type { ErroForcado } from '@/test/api-falsa'
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
  // servidor calcula o from com o relógio dele. Sabotagem: mandar também o
  // from no serie() do cliente → 400 da API falsa. Os indicadores pedem
  // também 24 h e 30 dias: a ordem não importa, o formato sim.
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
    expect(consultas()).toContainEqual({ window: '1h' })
    await r.usuario.click(screen.getByRole('radio', { name: '6 h' }))
    await waitFor(() => expect(consultas()).toContainEqual({ window: '6h' }))
    expect(consultas().filter((c) => 'from' in c)).toEqual([])
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
    expect(screen.getByRole('radio', { name: '7 dias' })).toBeChecked()
  })

  // Sem resposta do resumo, o dataUpdatedAt dele é 0: com esse relógio, um
  // incidente aberto há horas aparecia "há menos de 1 min". Sabotagem: passar
  // a IncidentesRecentes o relógio do resumo.
  it('sem o resumo, o tempo do incidente aberto vem da consulta dos incidentes', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.incidentes = [incidente({ started_at: new Date(Date.now() - 2 * 3_600_000).toISOString() })]
    await r.cliente.refetchQueries()
    servidor.use(
      http.get('*/api/v1/targets/summary', () =>
        HttpResponse.json({ error: { code: 'internal', message: 'falhou' }, trace_id: 't' }, { status: 500 }),
      ),
    )
    await r.cliente.resetQueries({ queryKey: ['targets', 'summary'], exact: true })
    const secao = await screen.findByRole('region', { name: 'Incidentes recentes' })
    expect(await within(secao).findByText(/há 2 h 0 min/)).toBeInTheDocument()
  })

  // "nunca" afirma que o alvo não tem check; sem o resumo, o painel só não
  // sabe. Sabotagem: tratar o item ausente como last_checked_at nulo.
  it('sem o resumo, o indicador de último check não diz "nunca"', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    servidor.use(
      http.get('*/api/v1/targets/summary', () =>
        HttpResponse.json({ error: { code: 'internal', message: 'falhou' }, trace_id: 't' }, { status: 500 }),
      ),
    )
    await r.cliente.resetQueries({ queryKey: ['targets', 'summary'], exact: true })
    const indicador = (await screen.findByText('Último check')).parentElement
    await waitFor(() => expect(indicador).toHaveTextContent('Indisponível'))
    expect(indicador).not.toHaveTextContent('nunca')
  })

  // A opacidade num bloco leva junto o texto dele: --suave a 60% dá 2,85:1 no
  // claro e 3,5:1 no escuro, abaixo do 4,5:1, e estes números não são um
  // controle inativo. O sinal é texto, depois do seletor, numa linha que já
  // existia: no celular, antes do seletor ele o empurraria logo após o toque.
  // Sabotagem: devolver o opacity-60 ao bloco da série, ou mudar o texto para
  // antes do seletor, ou tirar o anúncio.
  it('trocando de janela, os números ficam e o texto diz qual janela está na tela', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = {
      resolution: 'raw',
      items: [{ checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null }],
    }
    await r.cliente.refetchQueries()
    expect(await screen.findByText(/Mediana 100 ms/)).toBeInTheDocument()
    const linha = screen.getByTestId('troca-de-janela')
    expect(linha).toHaveClass('min-h-5')
    expect(linha).toBeEmptyDOMElement()
    const p = portao()
    servidor.use(
      http.get('*/api/v1/targets/:id/checks', async () => {
        await p.aberto
        return undefined
      }),
    )
    await r.usuario.click(screen.getByRole('radio', { name: '6 h' }))
    const sinal = await screen.findByText('Ainda em 24 h, carregando 6 h…')
    expect(sinal).toBe(linha)
    expect(screen.getByText(/Mediana 100 ms/).closest('[class*="opacity-"]')).toBeNull()
    expect(sinal.closest('[aria-busy="true"]')).toBeNull()
    const seletor = screen.getByRole('radiogroup', { name: 'Janela do gráfico' })
    expect(seletor.compareDocumentPosition(sinal) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    await waitFor(() => expect(screen.getByTestId('anuncios')).toHaveTextContent('Ainda em 24 h, carregando 6 h…'))
    p.soltar()
    await waitFor(() => expect(linha).toBeEmptyDOMElement())
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
    r.falsa.estado.forcar.set('GET /incidents', { status: 500, code: 'internal_error' })
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
    await r.usuario.click(screen.getByRole('radio', { name: '7 dias' }))
    const latencia = screen.getByRole('region', { name: 'Latência' })
    await waitFor(() => expect(within(latencia).getByText(/Mediana 100 ms/).closest('[aria-busy]')).toHaveAttribute('aria-busy', 'true'))
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
    r.falsa.estado.forcar.set('PATCH /targets/:id', { status: 500, code: 'internal_error' })
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
    expect(await screen.findByRole('radio', { name: '24 h' })).toBeChecked()
  })

  it('volta ao painel pelo link "Painel"', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('link', { name: 'Voltar ao painel' })).toHaveAttribute('href', '/')
  })

  // Spec, Telas > 2: quatro KPIs. 30 dias vem da janela horária; a mediana,
  // da janela de 24 h. Sabotagem: trocar useSerie(alvoId, '30d') por '24h'
  // → "No ar em 30 dias" mostra 66,66 %.
  it('os quatro indicadores: no ar em 24 h e em 30 dias, mediana de 24 h e último check', async () => {
    const r = await renderizarPainel('/targets/alvo-1?janela=1h')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ uptime_24h: 0.99999, last_checked_at: new Date(2026, 8, 24, 11, 59).toISOString() })]
    servidor.use(
      http.get('*/api/v1/targets/:id/checks', ({ request }) => {
        if (new URL(request.url).searchParams.get('window') === '30d') {
          return HttpResponse.json({
            resolution: '1h',
            items: [
              { bucket: '2026-09-24T10:00:00Z', total: 60, ok: 30, p50_ms: 120, p95_ms: 300, p99_ms: 400, max_ms: 500 },
              { bucket: '2026-09-24T11:00:00Z', total: 60, ok: 60, p50_ms: 120, p95_ms: 300, p99_ms: 400, max_ms: 500 },
            ],
          })
        }
        return HttpResponse.json({
          resolution: 'raw',
          items: [
            { checked_at: '2026-09-24T11:00:00Z', status: 'up', latency_ms: 100, status_code: 200, error_kind: null },
            { checked_at: '2026-09-24T11:01:00Z', status: 'up', latency_ms: 300, status_code: 200, error_kind: null },
            { checked_at: '2026-09-24T11:02:00Z', status: 'down', latency_ms: 10000, status_code: null, error_kind: 'timeout' },
          ],
        })
      }),
    )
    await r.cliente.refetchQueries()
    const valor = (rotulo: string) => screen.getByText(rotulo).nextElementSibling
    await waitFor(() => expect(valor('No ar em 30 dias')).toHaveTextContent('75,00 %'))
    expect(valor('No ar em 24 h')).toHaveTextContent('99,99 %')
    expect(valor('Latência mediana (24 h)')).toHaveTextContent('100 ms')
    expect(valor('Último check')).toHaveTextContent(/^11:59 há /)
  })

  // Sem check nenhum, a resposta veio e não há número: "Sem dados" e o motivo.
  it('alvo sem nenhum check: os indicadores dizem "Sem dados" e o motivo', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] })]
    await r.cliente.refetchQueries()
    await screen.findByRole('region', { name: 'Sem dados' })
    expect(screen.getByText('No ar em 24 h').nextElementSibling).toHaveTextContent('Sem dadosSem checks em 24 h')
    expect(screen.getByText('Último check').nextElementSibling).toHaveTextContent('Sem dadosNenhum check ainda')
  })

  // Com a consulta de verdade: o refetch sem dados passa por pending, e o <h1>
  // do detalhe só existe depois dos dados. Sabotagem: tirar o tabIndex do <main>
  // no Layout → o foco cai no <body> enquanto carrega.
  it('Tentar de novo sem dados: enquanto carrega, o foco fica no <main>', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    const botao = await screen.findByRole('button', { name: 'Tentar de novo' })
    r.falsa.estado.alvos = [alvo()]
    const p = portao()
    servidor.use(
      http.get('*/api/v1/targets/:id', async ({ params }) => {
        if (params.id === 'alvo-1') await p.aberto
        return undefined
      }),
    )
    await r.usuario.click(botao)
    await waitFor(() => expect(screen.getByRole('main')).toHaveFocus())
    expect(screen.queryByRole('heading', { level: 1 })).not.toBeInTheDocument()
    p.soltar()
    await screen.findByRole('heading', { name: 'API', level: 1 })
    expect(document.activeElement).not.toBe(document.body)
  })

  describe('resumo e indicadores sem resposta', () => {
    const erro500: ErroForcado = { status: 500, code: 'internal_error' }
    const valor = (rotulo: string) => screen.getByText(rotulo).nextElementSibling

    // Sabotagem: reaproveitar o aviso das tarefas (border-perigo) ou voltar ao
    // "noResumo && <Faixa>", sem aviso nenhum.
    it('o resumo falhou de vez: aviso neutro no lugar da faixa, anunciado, com o botão na coluna do texto', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      await r.cliente.refetchQueries({ queryKey: ['targets', 'alvo-1'], exact: true })
      r.falsa.estado.forcar.set('GET /targets/summary', erro500)
      await r.cliente.resetQueries({ queryKey: ['targets', 'summary'], exact: true })
      const aviso = await screen.findByRole('region', { name: 'Não foi possível saber o estado deste alvo.' })
      expect(aviso).toHaveClass('bg-sem-dados-100')
      expect(aviso).not.toHaveClass('flex-wrap')
      expect(aviso.className).not.toMatch(/border/)
      expect(aviso.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
      const botao = within(aviso).getByRole('button', { name: 'Tentar de novo' })
      expect(botao.parentElement).toContainElement(within(aviso).getByRole('heading'))
      expect(screen.queryByRole('region', { name: 'No ar' })).not.toBeInTheDocument()
      await waitFor(() => expect(screen.getByTestId('anuncios')).toHaveTextContent('Não foi possível saber o estado deste alvo.'))
    })

    // O pior caso de um monitor: o poll falha e a faixa segue dizendo "No ar".
    // Sabotagem: mostrar o aviso só com o resumo sem dados.
    it('o poll do resumo falhou: a faixa fica, com a hora do dado que ela mostra', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo()]
      await r.cliente.refetchQueries()
      await screen.findByRole('region', { name: 'No ar' })
      const hora = horaMinuto(new Date(r.cliente.getQueryState(['targets', 'summary'])?.dataUpdatedAt ?? 0).toISOString())
      r.falsa.estado.forcar.set('GET /targets/summary', erro500)
      await r.cliente.refetchQueries({ queryKey: ['targets', 'summary'], exact: true })
      const texto = `Não foi possível atualizar; mostrando dados de ${hora}.`
      expect(await screen.findByRole('region', { name: texto })).toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'No ar' })).toBeInTheDocument()
    })

    it('carregando o resumo: esqueleto da faixa, e um único aviso de carregando para o leitor de tela', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      await r.cliente.refetchQueries({ queryKey: ['targets', 'alvo-1'], exact: true })
      const p = portao()
      servidor.use(
        http.get('*/api/v1/targets/summary', async () => {
          await p.aberto
          return undefined
        }),
      )
      const leitura = r.cliente.resetQueries({ queryKey: ['targets', 'summary'], exact: true })
      await waitFor(() => expect(screen.getByTestId('esqueleto-da-faixa')).toBeInTheDocument())
      expect(screen.getAllByText('Carregando os indicadores…')).toHaveLength(1)
      expect(valor('No ar em 24 h')).toHaveTextContent('')
      expect(valor('No ar em 24 h')?.querySelector('[aria-hidden="true"]')).toHaveClass('animate-pulse', 'motion-reduce:animate-none')
      p.soltar()
      await leitura
      // Nada em voo no fim: com as buscas da montagem abortadas na desmontagem,
      // uma requisição do teste seguinte escapava da API falsa.
      await waitFor(() => expect(screen.queryByText('Carregando os indicadores…')).not.toBeInTheDocument())
    })

    // Os KPIs vêm de três consultas, e o aviso e o botão valem para cada uma.
    // Sabotagem: tirar dia.isError (ou mes.isError) do aviso, tirar uma das
    // consultas da lista das que falharam, ou refazer sempre as três.
    const SERIE = 'Não foi possível atualizar todos os indicadores.'
    it.each([
      { nome: 'o resumo', pedido: 'resumo', chave: ['targets', 'summary'], aviso: 'Não foi possível saber o estado deste alvo.', rotulo: 'No ar em 24 h' },
      { nome: 'a série de 24 h', pedido: '24h', chave: ['targets', 'alvo-1', 'checks', '24h'], aviso: SERIE, rotulo: 'Latência mediana (24 h)' },
      { nome: 'a série de 30 dias', pedido: '30d', chave: ['targets', 'alvo-1', 'checks', '30d'], aviso: SERIE, rotulo: 'No ar em 30 dias' },
    ] as const)('só $nome falhou: o cartão diz Indisponível, um aviso só, e o botão refaz só ela', async ({ pedido, chave, aviso: textoDoAviso, rotulo }) => {
      let falhar = true
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo()]
      // O resumo já em cache: nenhuma busca dele corre junto com a montagem.
      r.cliente.setQueryData(['targets', 'summary'], { items: [ItemDoResumo.parse(itemDoResumo())] })
      await r.cliente.refetchQueries({ queryKey: ['targets', 'alvo-1'], exact: true })
      await screen.findByRole('region', { name: 'No ar' }, { timeout: 3000 })
      // As duas séries já responderam: nenhuma busca delas fica em voo.
      await waitFor(() => expect(screen.queryByText('Carregando os indicadores…')).not.toBeInTheDocument())
      const ehAConsulta = (url: URL) => (pedido === 'resumo' ? url.pathname.endsWith('/targets/summary') : url.searchParams.get('window') === pedido)
      servidor.use(
        http.get('*/api/v1/targets/*', ({ request }) => {
          if (!falhar || !ehAConsulta(new URL(request.url))) return undefined
          return HttpResponse.json({ error: { code: 'internal', message: 'falhou' }, trace_id: 't' }, { status: 500 })
        }),
      )
      await r.cliente.resetQueries({ queryKey: chave, exact: true })
      const regiao = await screen.findByRole('region', { name: textoDoAviso }, { timeout: 3000 })
      await waitFor(() => expect(valor(rotulo)).toHaveTextContent('Indisponível'))
      expect(valor(rotulo)?.firstElementChild).toHaveClass('text-lg', 'leading-9', 'font-semibold', 'text-suave')
      // A mesma altura nas quatro formas: nada pula quando uma vira outra.
      expect(valor(rotulo)?.parentElement).toHaveClass('min-h-28')
      expect(within(regiao).getAllByRole('button')).toHaveLength(1)
      const dasSeries = (janela: string) => r.falsa.corpos('GET /targets/:id/checks').filter((c) => c.window === janela).length
      const pedidos = () => ({ resumo: r.falsa.chamadas('GET /targets/summary'), '24h': dasSeries('24h'), '30d': dasSeries('30d') })
      const antes = pedidos()
      falhar = false
      await r.usuario.click(within(regiao).getByRole('button', { name: 'Tentar de novo' }))
      await waitFor(() => expect(pedidos()).toEqual({ ...antes, [pedido]: antes[pedido] + 1 }))
      await waitFor(() => expect(screen.queryByRole('region', { name: textoDoAviso })).not.toBeInTheDocument())
      // O botão sumiu com o foco nele: o foco vai ao título da tela.
      await waitFor(() => expect(screen.getByRole('heading', { name: 'API', level: 1 })).toHaveFocus())
    })
  })

  // Review Focus 5. Sabotagem: tirar o ramo "s.items.length === 0" → a
  // tabela vazia aparece.
  it('janela sem checks: uma frase no lugar do gráfico e da tabela', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.serie = { resolution: 'raw', items: [] }
    await r.cliente.refetchQueries()
    const latencia = await screen.findByRole('region', { name: 'Latência' })
    expect(await within(latencia).findByText('Nenhum check nesta janela.')).toBeInTheDocument()
    expect(within(latencia).queryByTestId('grafico-latencia')).not.toBeInTheDocument()
    expect(within(latencia).queryByRole('table')).not.toBeInTheDocument()
    expect(within(latencia).getByText(/sem checks nesta janela/)).toBeInTheDocument()
  })

  it('a janela é um radiogroup: a seta escolhe a próxima e vai para a URL', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const grupo = await screen.findByRole('radiogroup', { name: 'Janela do gráfico' })
    expect(within(grupo).getByRole('radio', { name: '24 h' })).toBeChecked()
    await r.usuario.click(within(grupo).getByRole('radio', { name: '24 h' }))
    await r.usuario.keyboard('{ArrowRight}')
    expect(within(grupo).getByRole('radio', { name: '7 dias' })).toBeChecked()
    expect(r.router.state.location.search).toBe('?janela=7d')
  })

  // Spec, Telas > 2: linhas com selo (Em andamento, Reconhecido, Resolvido)
  // e a causa. Review Focus 7.
  it('incidentes recentes: selo, causa (ou "Causa não registrada") e período', async () => {
    const r = await renderizarPainel('/targets/alvo-1')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.incidentes = [incidente({ cause: '' }), incidente({ id: 'inc-2', resolved_at: '2026-09-24T11:30:00Z' })]
    await r.cliente.refetchQueries()
    const secao = await screen.findByRole('region', { name: 'Incidentes recentes' })
    await waitFor(() => expect(within(secao).getAllByRole('listitem')).toHaveLength(2))
    const [aberto, resolvido] = within(secao).getAllByRole('listitem') as [HTMLElement, HTMLElement]
    expect(within(aberto).getByText('Em andamento')).toBeInTheDocument()
    expect(within(aberto).getByText('Causa não registrada')).toBeInTheDocument()
    expect(within(resolvido).getByText('Resolvido')).toBeInTheDocument()
    expect(within(resolvido).getByText(/· durou 30 min$/)).toBeInTheDocument()
  })

  describe('faixa de estado', () => {
    // Spec, Telas > 2: a faixa diz há quanto tempo o alvo está fora, a causa
    // e o início, com Reconhecer. O foco vai ao "Reconhecido" quando o botão
    // some (o cuidado I1 da tela de incidentes). Sabotagem: tirar o
    // "focarDepois.current = true" → o foco cai no <body>.
    it('fora do ar com incidente aberto: tempo, causa e Reconhecer, que manda o ack e leva o foco', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
      r.falsa.estado.incidentes = [incidente({ cause: 'conn_refused' })]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: /^Fora do ar há / })
      expect(within(faixa).getByText(/^Conexão recusada · desde /)).toBeInTheDocument()
      const botao = within(faixa).getByRole('button', { name: 'Reconhecer' })
      expect(botao).toHaveAccessibleDescription('Reconhecer interrompe os lembretes; não resolve o incidente.')
      await r.usuario.click(botao)
      const feito = await within(faixa).findByText(/lembretes parados desde/)
      await waitFor(() => expect(feito).toHaveFocus())
      expect(within(faixa).queryByRole('button', { name: 'Reconhecer' })).not.toBeInTheDocument()
      expect(r.falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
    })

    it('sem incidente aberto, a faixa diz o estado e o último check, sem Reconhecer', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo()]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: 'No ar' })
      expect(within(faixa).getByText(/^Último check /)).toBeInTheDocument()
      expect(within(faixa).queryByRole('button')).not.toBeInTheDocument()
    })

    it('pausado: a faixa diz que os checks estão parados', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo({ enabled: false })]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'unknown', enabled: false })]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: 'Pausado' })
      expect(within(faixa).getByText('Os checks estão parados. Retome para voltar a vigiar.')).toBeInTheDocument()
    })

    // Review Focus 4.
    it('alvo sem nenhum check: "Sem dados" e "Nenhum check ainda."', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] })]
      await r.cliente.refetchQueries()
      const faixa = await screen.findByRole('region', { name: 'Sem dados' })
      expect(within(faixa).getByText('Nenhum check ainda.')).toBeInTheDocument()
    })

    // M3: o incidente sumiu com o alvo, apagado em outra aba. Sabotagem:
    // tirar o ramo do naoEncontrado em reconhecer() → o erro cru fica na tela.
    it('o ack com 404 explica e volta à lista', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
      r.falsa.estado.incidentes = [incidente()]
      await r.cliente.refetchQueries()
      const botao = await screen.findByRole('button', { name: 'Reconhecer' })
      r.falsa.estado.incidentes = []
      await r.usuario.click(botao)
      expect((await screen.findAllByText('O alvo já tinha sido apagado, talvez em outra aba.'))[0]).toBeInTheDocument()
      await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    })

    it('um ack que falha mostra o erro e mantém o Reconhecer', async () => {
      const r = await renderizarPainel('/targets/alvo-1')
      r.falsa.estado.alvos = [alvo()]
      r.falsa.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
      r.falsa.estado.incidentes = [incidente()]
      await r.cliente.refetchQueries()
      r.falsa.estado.forcar.set('POST /incidents/:id/ack', { status: 503, code: 'unavailable' })
      await r.usuario.click(await screen.findByRole('button', { name: 'Reconhecer' }))
      expect(await screen.findByRole('alert')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Reconhecer' })).toBeInTheDocument()
    })
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

