import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { dataHora } from '@/lib/format'
import { canal } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

const RESEND = 'POST /channels/:id/verify/resend'
const escapar = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// Abre o diálogo, escolhe e-mail e manda o endereço.
async function cadastrarEmail(r: Awaited<ReturnType<typeof renderizarPainel>>, email: string) {
  await r.usuario.click(await screen.findByRole('button', { name: 'Novo canal' }))
  const dialogo = await screen.findByRole('dialog')
  await r.usuario.click(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=radio]' }))
  await r.usuario.type(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=email]' }), email)
  await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
  return dialogo
}

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
    const falha = screen.getByText(/Falha: Canal inválido \(o webhook foi apagado\?\)/)
    expect(falha).toHaveTextContent(new RegExp(`, em ${escapar(dataHora('2026-09-24T11:00:00Z'))}$`))
    expect(falha).toHaveTextContent('24/09/2026')
    // Reenviar só aparece no pendente, com o canal no nome acessível.
    expect(screen.getAllByRole('button', { name: /^Reenviar verificação/ })).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })).toBeInTheDocument()
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

  it('verificação expirada tem símbolo e texto, e pode ser reenviada', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'expired' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('⧖ Verificação expirada')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })).toBeInTheDocument()
  })

  it('e-mail que o servidor aceita chega ao POST, e o 400 email/invalid vira texto', async () => {
    const r = await renderizarPainel('/channels')
    await cadastrarEmail(r, 'josé@exemplo.com')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await cadastrarEmail(r, 'dono@münchen.de')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(r.falsa.corpos('POST /channels')).toEqual([
      { kind: 'email', email: 'josé@exemplo.com' },
      { kind: 'email', email: 'dono@münchen.de' },
    ])

    r.falsa.estado.forcar.set('POST /channels', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'email', reason: 'invalid' }],
    })
    const dialogo = await cadastrarEmail(r, 'dono@x')
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('E-mail inválido.')
    expect(r.falsa.corpos('POST /channels')[2]).toEqual({ kind: 'email', email: 'dono@x' })
  })

  it('criar: limite de canais, limite de envios e URL de webhook inválida viram texto', async () => {
    const r = await renderizarPainel('/channels')
    await r.usuario.click(await screen.findByRole('button', { name: 'Novo canal' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    const casos: [{ status: number; code: string; fields?: { field: string; reason: string }[] }, string][] = [
      [{ status: 409, code: 'channel_limit_reached' }, 'Limite de 10 canais atingido.'],
      [{ status: 429, code: 'rate_limited' }, 'Limite de envios atingido (1 a cada 6 minutos): tente de novo em alguns minutos.'],
      [
        { status: 400, code: 'validation_failed', fields: [{ field: 'url', reason: 'invalid' }] },
        'Use a URL do webhook do Discord (https://discord.com/api/webhooks/…).',
      ],
    ]
    for (const [forcado, texto] of casos) {
      r.falsa.estado.forcar.set('POST /channels', forcado)
      await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
      await waitFor(() => expect(within(dialogo).getByRole('alert')).toHaveTextContent(texto))
    }
  })

  it('reenviar: um envio só, mesmo com dois cliques', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'pending' })]
    await r.cliente.refetchQueries()
    const botao = await screen.findByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })
    fireEvent.click(botao)
    fireEvent.click(botao)
    // Os dois cliques já saíram; um aviso por envio.
    expect((await screen.findAllByText('Link de verificação reenviado.'))[0]).toBeInTheDocument()
    expect(r.falsa.chamadas(RESEND)).toBe(1)
    await waitFor(() => expect(botao).toBeEnabled())
  })

  it('reenviar: limite de envios, e-mail não configurado e entrega que falha viram texto', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'pending' })]
    await r.cliente.refetchQueries()
    const botao = await screen.findByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })
    const casos: [{ status: number; code: string; fields?: { field: string; reason: string }[] }, string][] = [
      [{ status: 429, code: 'rate_limited' }, 'Limite de envios atingido (1 a cada 6 minutos): tente de novo em alguns minutos.'],
      [
        { status: 400, code: 'validation_failed', fields: [{ field: 'kind', reason: 'unavailable' }] },
        'O envio de e-mail não está configurado neste servidor.',
      ],
      [{ status: 502, code: 'delivery_failed' }, 'Não conseguimos entregar o e-mail de verificação. Tente de novo mais tarde.'],
    ]
    for (const [forcado, texto] of casos) {
      r.falsa.estado.forcar.set(RESEND, forcado)
      await r.usuario.click(botao)
      await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(texto))
    }
    expect(screen.queryByText(/mensagem de teste/)).not.toBeInTheDocument()
  })

  it('reenviar de canal que já não está pendente, ou que foi apagado, recarrega a lista e explica', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal({ id: 'c1', kind: 'email', hint: 'a***@exemplo.com', status: 'pending' }),
      canal({ id: 'c2', kind: 'email', hint: 'b***@exemplo.com', status: 'pending' }),
    ]
    await r.cliente.refetchQueries()
    const primeiro = await screen.findByRole('button', { name: 'Reenviar verificação do canal a***@exemplo.com' })

    // Verificado em outra aba.
    r.falsa.estado.canais = [
      canal({ id: 'c1', kind: 'email', hint: 'a***@exemplo.com', status: 'verified' }),
      canal({ id: 'c2', kind: 'email', hint: 'b***@exemplo.com', status: 'pending' }),
    ]
    r.falsa.estado.forcar.set(RESEND, { status: 409, code: 'channel_not_pending' })
    await r.usuario.click(primeiro)
    expect(await screen.findByText('O canal não está mais aguardando verificação.')).toBeInTheDocument()
    expect(await screen.findByText('✓ Verificado')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reenviar verificação do canal a***@exemplo.com' })).not.toBeInTheDocument()

    // Apagado em outra aba.
    r.falsa.estado.canais = [canal({ id: 'c1', kind: 'email', hint: 'a***@exemplo.com', status: 'verified' })]
    r.falsa.estado.forcar.set(RESEND, { status: 404, code: 'not_found' })
    await r.usuario.click(screen.getByRole('button', { name: 'Reenviar verificação do canal b***@exemplo.com' }))
    expect(await screen.findByText('O canal já tinha sido apagado, talvez em outra aba.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('b***@exemplo.com')).not.toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('apagar pelo Confirmar: um DELETE só, e o erro anterior some', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'pending' })]
    await r.cliente.refetchQueries()
    // Um erro de antes na tela.
    r.falsa.estado.forcar.set(RESEND, { status: 429, code: 'rate_limited' })
    await r.usuario.click(await screen.findByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Limite de envios atingido')

    await r.usuario.click(screen.getByRole('button', { name: 'Apagar o canal d***@exemplo.com' }))
    const confirmar = await screen.findByRole('alertdialog')
    const acao = within(confirmar).getByRole('button', { name: 'Apagar canal' })
    fireEvent.click(acao)
    fireEvent.click(acao)
    expect((await screen.findAllByText('Canal apagado.'))[0]).toBeInTheDocument()
    expect(r.falsa.chamadas('DELETE /channels/:id')).toBe(1)
    expect(await screen.findByText(/Nenhum canal/)).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('apagar canal que outra aba já apagou recarrega a lista e explica', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar o canal discord.com/…/abcd' }))
    r.falsa.estado.canais = []
    r.falsa.estado.forcar.set('DELETE /channels/:id', { status: 404, code: 'not_found' })
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Apagar canal' }))
    expect(await screen.findByText('O canal já tinha sido apagado, talvez em outra aba.')).toBeInTheDocument()
    expect(await screen.findByText(/Nenhum canal/)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // I1 da revisão final: o "Apagar" que abriu o diálogo sumiu com a linha;
  // o foco vai ao título da tela, e não ao <body>. Sabotagem: a de
  // dialogo.test.tsx (a lista só com o gatilho) → o foco cai no body.
  it('depois de apagar, o foco não cai no <body>', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal(), canal({ id: 'canal-2', hint: 'discord.com/…/efgh' })]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar o canal discord.com/…/abcd' }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Apagar canal' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(document.activeElement).not.toBe(document.body))
    expect(screen.getByRole('heading', { name: 'Canais de notificação', level: 1 })).toHaveFocus()
  })
})

