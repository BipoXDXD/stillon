import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { incidente } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

describe('incidentes', () => {
  it('lista os abertos por padrão, com causa em texto, e reconhece', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [
      incidente(),
      incidente({ id: 'inc-2', resolved_at: '2026-09-24T11:30:00Z', target_name: 'Site' }),
    ]
    await r.cliente.refetchQueries()
    const lista = (await screen.findByText(/Tempo esgotado/)).closest('ul') as HTMLElement
    expect(within(lista).getAllByRole('listitem')).toHaveLength(1)
    expect(within(lista).getByText(/Tempo esgotado/)).toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Reconhecer o incidente de API' }))
    expect(await screen.findByText(/Reconhecido em/)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
  })

  it('o filtro vai para a URL e muda a consulta', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente({ resolved_at: '2026-09-24T11:30:00Z', target_name: 'Site' })]
    await r.usuario.click(await screen.findByRole('button', { name: 'Resolvidos' }))
    expect(r.router.state.location.search).toBe('?status=resolved')
    expect(await screen.findByText(/durou 30 min/)).toBeInTheDocument()
    // Resolvido não se reconhece.
    expect(screen.queryByRole('button', { name: /Reconhecer/ })).not.toBeInTheDocument()
  })

  it('"Carregar mais" segue o next_cursor', async () => {
    const r = await renderizarPainel('/incidents?status=all')
    r.falsa.estado.incidentes = Array.from({ length: 60 }, (_, i) =>
      incidente({ id: `inc-${String(i).padStart(2, '0')}`, target_name: `Alvo ${i}` }),
    )
    await r.cliente.refetchQueries()
    const itens = () => screen.getAllByRole('listitem').filter((li) => li.textContent?.includes('Tempo esgotado'))
    await waitFor(() => expect(itens()).toHaveLength(50))
    await r.usuario.click(screen.getByRole('button', { name: 'Carregar mais' }))
    await waitFor(() => expect(itens()).toHaveLength(60))
    expect(screen.queryByRole('button', { name: 'Carregar mais' })).not.toBeInTheDocument()
  })
})
