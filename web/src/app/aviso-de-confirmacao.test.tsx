import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { chaves } from '@/lib/api/consultas'
import { EMAIL, SENHA } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

const TEXTO = 'Confirme seu e-mail para criar monitores, canais e páginas.'
const PEDIDO_ACEITO = 'Pedido de reenvio recebido. Se o e-mail ainda precisar de confirmação, confira sua caixa de entrada.'

function aviso(): HTMLElement | null {
  return screen.queryByRole('complementary', { name: 'Confirmação de e-mail' })
}

function avisoNaTela(): HTMLElement {
  const el = aviso()
  if (!el) throw new Error('o aviso de e-mail não confirmado não está na tela')
  return el
}

// O painel aberto com a conta ainda não confirmada: o /me volta com
// email_verified false.
async function painelNaoConfirmado() {
  const r = await renderizarPainel('/')
  await screen.findByRole('heading', { name: 'Painel' })
  r.falsa.estado.emailConfirmado = false
  await act(() => r.cliente.invalidateQueries({ queryKey: chaves.me }))
  await waitFor(() => avisoNaTela())
  return r
}

afterEach(() => {
  vi.useRealTimers()
})

describe('aviso de e-mail não confirmado', () => {
  // Sabotagem prevista: tirar o e-mail do texto, ou o botão do aviso.
  it('mostra o e-mail e o botão de reenviar', async () => {
    await painelNaoConfirmado()
    const el = avisoNaTela()
    expect(el).toHaveTextContent(`${TEXTO} Enviamos um link para ${EMAIL}.`)
    expect(within(el).getByText(EMAIL).tagName).toBe('STRONG')
    expect(within(el).getByRole('button', { name: 'Reenviar e-mail' })).toBeInTheDocument()
    // No topo do conteúdo, antes do título da tela.
    const main = screen.getByRole('main')
    expect(main).toContainElement(el)
    const titulo = screen.getByRole('heading', { name: 'Painel' })
    expect(el.compareDocumentPosition(titulo) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // Não rouba o foco: a carga da tela não o leva ao aviso.
    expect(el).not.toContainElement(document.activeElement as HTMLElement | null)
  })

  it.each([false, true])('o botão de reenviar some e volta após 60 s (foco movido: %s)', async (moverFoco) => {
    const { api, falsa } = await painelNaoConfirmado()
    const faixa = within(avisoNaTela())
    const acao = faixa.getByRole('button', { name: 'Reenviar e-mail' })
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const reenviar = vi.spyOn(api, 'reenviarConfirmacao')
    acao.focus()
    fireEvent.click(acao)
    await act(async () => {
      await reenviar.mock.results[0]?.value
    })

    const confirmacao = faixa.getByText(PEDIDO_ACEITO)
    expect(falsa.chamadas('POST /auth/email/resend')).toBe(1)
    expect(falsa.estado.cadastro.tokens.size).toBe(1)
    expect(faixa.queryByRole('button')).not.toBeInTheDocument()
    expect(confirmacao).toHaveFocus()
    const titulo = screen.getByRole('heading', { name: 'Painel' })
    if (moverFoco) titulo.focus()

    act(() => {
      vi.advanceTimersByTime(59_999)
    })
    expect(faixa.queryByRole('button')).not.toBeInTheDocument()
    act(() => {
      vi.advanceTimersByTime(1)
    })
    const botao = faixa.getByRole('button', { name: 'Reenviar e-mail' })
    expect(avisoNaTela()).not.toHaveTextContent('Pedido de reenvio recebido.')
    expect(moverFoco ? titulo : botao).toHaveFocus()
  })

  it('a região de status permanece montada quando o reenvio é aceito sem gerar um link', async () => {
    const { falsa, usuario } = await painelNaoConfirmado()
    // O painel ainda exibe a faixa depois da confirmação em outro aparelho.
    // Nesse caso a API aceita o pedido, mas não gera um link novo.
    falsa.estado.emailConfirmado = true
    const faixa = within(avisoNaTela())
    const status = faixa.getByRole('status')
    const botao = faixa.getByRole('button', { name: 'Reenviar e-mail' })
    expect(status).toBeEmptyDOMElement()
    expect(status).not.toContainElement(botao)

    await usuario.click(botao)
    await waitFor(() => expect(status.textContent).toBe(PEDIDO_ACEITO))
    expect(falsa.estado.cadastro.tokens.size).toBe(0)
    expect(faixa.getByRole('status')).toBe(status)
  })

  // O texto de rate_limited com o tempo do Retry-After (mensagemDe), e o
  // botão continua. Sabotagem prevista: mostrar a confirmação também no erro.
  it('erro 429 mostra a mensagem de limite', async () => {
    const { usuario } = await painelNaoConfirmado()
    servidor.use(
      http.post('*/api/v1/auth/email/resend', () =>
        HttpResponse.json(
          { error: { code: 'rate_limited', message: 'limite' }, trace_id: 't' },
          { status: 429, headers: { 'Retry-After': '120' } },
        ),
      ),
    )
    const faixa = within(avisoNaTela())
    const status = faixa.getByRole('status')
    const botao = faixa.getByRole('button', { name: 'Reenviar e-mail' })
    await usuario.click(botao)
    await waitFor(() =>
      expect(status).toHaveTextContent(/^Muitas tentativas\. Aguarde 2 minutos e tente de novo\.$/),
    )
    expect(faixa.getByRole('status')).toBe(status)
    expect(botao).toHaveAccessibleName('Reenviar e-mail')
    expect(botao).not.toHaveAttribute('aria-disabled')
    expect(botao).toHaveFocus()
  })

  // Coordenação com a T11: a confirmação invalida o /me, e o painel deixa de
  // mostrar o aviso (o /me tem staleTime Infinity). Sabotagem prevista: tirar
  // o invalidateQueries de PaginaConfirmarEmail.
  it('depois de confirmar o e-mail, o painel não mostra mais o aviso', async () => {
    const { falsa, router, usuario } = await painelNaoConfirmado()
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    await act(() => router.navigate('/confirm-email#token=token-da-caixa'))
    await usuario.type(await screen.findByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    await screen.findByRole('heading', { name: 'E-mail confirmado' })
    await act(() => router.navigate('/'))
    await screen.findByRole('heading', { name: 'Painel' })
    await waitFor(() => expect(aviso()).toBeNull())
  })
})
