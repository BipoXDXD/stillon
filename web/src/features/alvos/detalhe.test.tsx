import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo, incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

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
})
