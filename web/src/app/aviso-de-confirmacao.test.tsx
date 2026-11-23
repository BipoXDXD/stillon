import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { chaves } from '@/lib/api/consultas'
import { EMAIL } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

const TEXTO = 'Confirme seu e-mail para criar monitores, canais e páginas.'

// O aviso é a região de status (<output>) com o texto aprovado (proposta da T11, §8).
function aviso(): HTMLElement | null {
  return (
    screen
      .queryAllByRole('status')
      .find((el) => el.textContent?.includes(TEXTO)) ?? null
  )
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

  // Quatro sabotagens previstas: não chamar a API (nenhum POST), trocar 60 s
  // por 30 s (o botão volta cedo), não levar o foco ao texto (cai no <body>
  // quando o botão some) e não devolvê-lo ao botão depois da espera.
  it('reenviar chama a API, confirma o envio e desabilita o botão por 60 s', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'], shouldAdvanceTime: true })
    const { falsa, usuario } = await painelNaoConfirmado()
    await usuario.click(within(avisoNaTela()).getByRole('button', { name: 'Reenviar e-mail' }))

    await waitFor(() => expect(avisoNaTela()).toHaveTextContent('Link reenviado. Confira sua caixa de entrada.'))
    expect(falsa.chamadas('POST /auth/email/resend')).toBe(1)
    expect(falsa.estado.cadastro.tokens.size).toBe(1)
    expect(within(avisoNaTela()).queryByRole('button', { name: 'Reenviar e-mail' })).not.toBeInTheDocument()
    expect(document.body).not.toHaveFocus()
    expect(avisoNaTela()).toContainElement(document.activeElement as HTMLElement | null)

    act(() => {
      vi.advanceTimersByTime(59_000)
    })
    expect(within(avisoNaTela()).queryByRole('button', { name: 'Reenviar e-mail' })).not.toBeInTheDocument()
    act(() => {
      vi.advanceTimersByTime(1_000)
    })
    const botao = within(avisoNaTela()).getByRole('button', { name: 'Reenviar e-mail' })
    expect(avisoNaTela()).not.toHaveTextContent('Link reenviado.')
    // O foco estava no texto: volta ao botão, e não ao <body>.
    expect(botao).toHaveFocus()
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
    await usuario.click(within(avisoNaTela()).getByRole('button', { name: 'Reenviar e-mail' }))
    await waitFor(() =>
      expect(avisoNaTela()).toHaveTextContent('Muitas tentativas. Aguarde 2 minutos e tente de novo.'),
    )
    expect(avisoNaTela()).not.toHaveTextContent('Link reenviado.')
    expect(within(avisoNaTela()).getByRole('button', { name: 'Reenviar e-mail' })).toBeInTheDocument()
  })

  // Coordenação com a T11: a confirmação invalida o /me, e o painel deixa de
  // mostrar o aviso (o /me tem staleTime Infinity). Sabotagem prevista: tirar
  // o invalidateQueries de PaginaConfirmarEmail.
  it('depois de confirmar o e-mail, o painel não mostra mais o aviso', async () => {
    const { falsa, router } = await painelNaoConfirmado()
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    await act(() => router.navigate('/confirm-email#token=token-da-caixa'))
    await screen.findByRole('heading', { name: 'E-mail confirmado' })
    await act(() => router.navigate('/'))
    await screen.findByRole('heading', { name: 'Painel' })
    await waitFor(() => expect(aviso()).toBeNull())
  })
})
