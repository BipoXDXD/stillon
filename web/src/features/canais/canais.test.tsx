import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { canal } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

describe('canais', () => {
  it('mostra cada estado em texto, e a falha com motivo e data', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal(),
      canal({ id: 'c2', kind: 'email', hint: 'd***@exemplo.com', status: 'pending' }),
      canal({
        id: 'c3',
        hint: 'discord.com/…/zzzz',
        status: 'failing',
        last_error: 'invalid_channel',
        last_failure_at: '2026-09-24T11:00:00Z',
      }),
    ]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('✓ Verificado')).toBeInTheDocument()
    expect(screen.getByText('… Aguardando verificação')).toBeInTheDocument()
    expect(screen.getByText('✕ Falhando')).toBeInTheDocument()
    expect(screen.getByText(/Falha: Canal inválido \(o webhook foi apagado\?\)/)).toBeInTheDocument()
    // Reenviar só aparece no pendente.
    expect(screen.getAllByRole('button', { name: 'Reenviar verificação' })).toHaveLength(1)
  })

  it('cadastrar Discord verifica com a mensagem de teste', async () => {
    const r = await renderizarPainel('/channels')
    await r.usuario.click(await screen.findByRole('button', { name: 'Novo canal' }))
    const dialogo = await screen.findByRole('dialog')
    expect(within(dialogo).getByText(/Webhook de canal de fórum não é aceito/)).toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(r.falsa.corpos('POST /channels')[0]).toEqual({ kind: 'discord', url: 'https://discord.com/api/webhooks/1/abc' })
    expect(await screen.findByText('Canal verificado com a mensagem de teste.')).toBeInTheDocument()
  })

  it('entrega que falha e e-mail não configurado viram texto claro', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.forcar.set('POST /channels', { status: 502, code: 'delivery_failed' })
    await r.usuario.click(await screen.findByRole('button', { name: 'Novo canal' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('Não conseguimos entregar a mensagem de teste')

    r.falsa.estado.forcar.set('POST /channels', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'kind', reason: 'unavailable' }],
    })
    await r.usuario.click(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=radio]' }))
    await r.usuario.type(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=email]' }), 'dono@exemplo.com')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    await waitFor(() =>
      expect(within(dialogo).getByRole('alert')).toHaveTextContent('O envio de e-mail não está configurado'),
    )
    expect(r.falsa.corpos('POST /channels')[1]).toEqual({ kind: 'email', email: 'dono@exemplo.com' })
  })
})
