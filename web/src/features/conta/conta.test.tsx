import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { chaves } from '@/lib/api/consultas'
import { EMAIL, SENHA, SITE_KEY_DO_TURNSTILE, TOKEN_DO_TURNSTILE } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'
import { esquecerOScriptDoTurnstile, URL_DO_TURNSTILE, type OpcoesDoTurnstile } from './Turnstile'

// As telas públicas do cadastro contra a API falsa, com um window.turnstile
// de mentira: o teste faz o papel da Cloudflare chamando os callbacks.
function instalarTurnstile() {
  const widgets: OpcoesDoTurnstile[] = []
  const falso = {
    render: vi.fn<(alvo: HTMLElement, o: OpcoesDoTurnstile) => string | undefined>((_alvo, o) => {
      widgets.push(o)
      return `w${widgets.length}`
    }),
    reset: vi.fn<(id: string) => void>(),
    remove: vi.fn<(id: string) => void>(),
  }
  window.turnstile = falso
  function ultimo() {
    const w = widgets.at(-1)
    if (!w) throw new Error('nenhum widget desenhado')
    return w
  }
  return {
    ...falso,
    widgets,
    // Espera o widget existir e o resolve com o token.
    async resolver(token = TOKEN_DO_TURNSTILE) {
      await waitFor(() => expect(falso.render).toHaveBeenCalled())
      act(() => ultimo().callback(token))
    },
    expirar: () => act(() => ultimo()['expired-callback']()),
  }
}

// As URLs de todas as requisições: o token de conta nunca vai numa query.
const urls: string[] = []
const anotar = ({ request }: { request: Request }) => {
  urls.push(request.url)
}
let turnstile: ReturnType<typeof instalarTurnstile>

beforeEach(() => {
  urls.length = 0
  servidor.events.on('request:start', anotar)
  turnstile = instalarTurnstile()
})
afterEach(() => {
  servidor.events.removeListener('request:start', anotar)
  delete window.turnstile
  document.querySelector(`script[src="${URL_DO_TURNSTILE}"]`)?.remove()
  esquecerOScriptDoTurnstile()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const SENHA_NOVA = 'uma senha nova bem comprida'
const NOVO = 'nova@exemplo.com'

async function preencherCadastro(usuario: Awaited<ReturnType<typeof renderizarPainel>>['usuario'], senha = SENHA_NOVA) {
  await screen.findByRole('heading', { name: 'Criar conta no StillOn' })
  await usuario.type(await screen.findByLabelText('E-mail'), NOVO)
  await usuario.type(screen.getByLabelText('Senha'), senha)
}

describe('cadastro', () => {
  // Sabotagem prevista: o disabled do botão olhar só o isSubmitting.
  it('cadastro só habilita o botão com o desafio resolvido', async () => {
    const { usuario } = await renderizarPainel('/signup', { logado: false })
    await preencherCadastro(usuario)
    const botao = screen.getByRole('button', { name: 'Criar conta' })
    expect(botao).toBeDisabled()
    expect(botao).toHaveAccessibleDescription('Aguarde a verificação acima.')
    await turnstile.resolver()
    expect(turnstile.render.mock.calls[0]?.[1]).toMatchObject({ sitekey: SITE_KEY_DO_TURNSTILE, action: 'signup' })
    expect(botao).toBeEnabled()
    expect(screen.queryByText('Aguarde a verificação acima.')).not.toBeInTheDocument()
    // O token venceu (300 s): o botão volta a esperar o desafio.
    turnstile.expirar()
    expect(botao).toBeDisabled()
  })

  it('cadastro mostra o estado de confira seu e-mail depois do 202', async () => {
    const { usuario, falsa } = await renderizarPainel('/signup', { logado: false })
    await preencherCadastro(usuario)
    await turnstile.resolver()
    await usuario.click(screen.getByRole('button', { name: 'Criar conta' }))
    const titulo = await screen.findByRole('heading', { name: 'Confira seu e-mail' })
    expect(titulo).toHaveFocus()
    expect(falsa.corpos('POST /auth/signup')).toEqual([
      { email: NOVO, password: SENHA_NOVA, turnstile_token: TOKEN_DO_TURNSTILE },
    ])
    const main = screen.getByRole('main')
    expect(main).toHaveTextContent(`Enviamos um link para ${NOVO}, se ele puder receber mensagens do StillOn.`)
    expect(main).toHaveTextContent('Abra o link em até 24 horas para concluir o cadastro.')
    expect(main).toHaveTextContent('Não chegou em alguns minutos? Confira a caixa de spam ou peça de novo.')
    expect(screen.getByRole('link', { name: 'Voltar para entrar' })).toHaveAttribute('href', '/login')
  })

  // A API falsa recusa o token que não é o das chaves de teste, como o
  // siteverify. Sabotagem prevista: não incrementar o reiniciar no erro.
  it('cadastro com turnstile_failed mostra a mensagem e reinicia o widget', async () => {
    const { usuario } = await renderizarPainel('/signup', { logado: false })
    await preencherCadastro(usuario)
    await turnstile.resolver('token-recusado')
    await usuario.click(screen.getByRole('button', { name: 'Criar conta' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'A verificação de segurança falhou. Faça a verificação de novo e envie.',
    )
    expect(turnstile.reset).toHaveBeenCalledWith('w1')
    expect(screen.getByRole('button', { name: 'Criar conta' })).toBeDisabled()
    expect(screen.getByLabelText('Senha')).toHaveValue(SENHA_NOVA)
  })

  // Duas sabotagens previstas: tirar o mínimo do zod (o POST sai) e tirar
  // porCampo.password de mensagens.ts (o 400 do servidor vira "Curto demais.").
  it('cadastro mostra o erro de senha curta no campo', async () => {
    const { usuario, falsa } = await renderizarPainel('/signup', { logado: false })
    await preencherCadastro(usuario, 'curta demais')
    await turnstile.resolver()
    await usuario.click(screen.getByRole('button', { name: 'Criar conta' }))
    const senha = screen.getByLabelText('Senha')
    await waitFor(() => expect(senha).toHaveAttribute('aria-invalid', 'true'))
    expect(senha).toHaveAccessibleDescription(expect.stringContaining('Mínimo de 15 caracteres.'))
    expect(falsa.chamadas('POST /auth/signup')).toBe(0)

    // O servidor conta em code points; se recusar, o erro vai ao mesmo campo.
    falsa.estado.forcar.set('POST /auth/signup', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'password', reason: 'too_short' }],
    })
    await usuario.clear(senha)
    await usuario.type(senha, SENHA_NOVA)
    await waitFor(() => expect(senha).not.toHaveAttribute('aria-invalid'))
    await usuario.click(screen.getByRole('button', { name: 'Criar conta' }))
    await waitFor(() => expect(senha).toHaveAttribute('aria-invalid', 'true'))
    expect(senha).toHaveAccessibleDescription(expect.stringContaining('Mínimo de 15 caracteres.'))
    expect(senha).toHaveFocus()
    expect(falsa.chamadas('POST /auth/signup')).toBe(1)
  })

  // O boot que termina anônimo limpa o cache do React Query. Uma config
  // buscada antes dele sumia no meio da tela: o formulário voltava a
  // "Carregando…" e o widget era recriado. Sabotagem prevista: tirar o
  // enabled do useConfigDoCadastro.
  it('a config espera o boot da sessão, e o formulário não volta a carregar', async () => {
    const { usuario, falsa, sessao } = await renderizarPainel('/signup', { logado: false, atrasoDoBoot: 100 })
    expect(screen.getByText('Carregando…')).toBeInTheDocument()
    await waitFor(() => expect(sessao.estado()).toBe('anonimo'))
    await preencherCadastro(usuario)
    expect(screen.getByLabelText('Senha')).toHaveValue(SENHA_NOVA)
    expect(screen.queryByText('Carregando…')).not.toBeInTheDocument()
    expect(falsa.chamadas('GET /auth/signup-config')).toBe(1)
    expect(turnstile.render).toHaveBeenCalledTimes(1)
  })

  it('mostrar senha alterna o tipo do campo e o estado do botão', async () => {
    const { usuario } = await renderizarPainel('/signup', { logado: false })
    await preencherCadastro(usuario)
    const mostrar = screen.getByRole('button', { name: 'Mostrar senha' })
    expect(mostrar).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByLabelText('Senha')).toHaveAttribute('type', 'password')
    await usuario.click(mostrar)
    expect(mostrar).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('Senha')).toHaveAttribute('type', 'text')
  })
})

describe('verificação de segurança', () => {
  it.each(['script sem API', 'render lança', 'render sem id', 'callback de erro'] as const)(
    'anuncia a falha e troca a orientação ao usuário quando %s',
    async (falha) => {
      if (falha === 'script sem API') delete window.turnstile
      if (falha === 'render lança') turnstile.render.mockImplementationOnce(() => { throw new Error('Falha do provedor') })
      if (falha === 'render sem id') turnstile.render.mockReturnValueOnce(undefined)

      await renderizarPainel('/forgot-password', { logado: false })
      const botao = await screen.findByRole('button', { name: 'Enviar link' })
      if (falha === 'script sem API') {
        const script = document.querySelector(`script[src="${URL_DO_TURNSTILE}"]`)
        await act(async () => { fireEvent.load(script!) })
      }
      if (falha === 'callback de erro') {
        await turnstile.resolver()
        act(() => turnstile.widgets.at(-1)!['error-callback']())
      }

      const orientacao = 'Não foi possível carregar a verificação. Desative bloqueadores para este site e recarregue a página.'
      expect((await screen.findByText(orientacao)).closest('[aria-live="assertive"]')).not.toBeNull()
      expect(botao).toBeDisabled()
      expect(botao).toHaveAccessibleDescription(orientacao)
      expect(screen.queryByText('Aguarde a verificação acima.')).not.toBeInTheDocument()
    },
  )

  it('muda o tamanho do widget com a largura disponível e invalida o desafio anterior', async () => {
    let largura = 238
    let redimensionar: (() => void) | undefined
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(() => largura)
    vi.stubGlobal('ResizeObserver', class {
      constructor(aoRedimensionar: () => void) { redimensionar = aoRedimensionar }
      observe() {}
      disconnect() {}
    })

    await renderizarPainel('/signup', { logado: false })
    const botao = await screen.findByRole('button', { name: 'Criar conta' })
    await turnstile.resolver()
    expect(turnstile.widgets[0]?.size).toBe('compact')
    expect(botao).toBeEnabled()

    largura = 310
    await act(async () => redimensionar?.())
    expect(turnstile.widgets[1]?.size).toBe('flexible')
    expect(botao).toBeDisabled()
    act(() => turnstile.widgets[0]!.callback('token-do-widget-removido'))
    expect(botao).toBeDisabled()
    await turnstile.resolver()
    expect(botao).toBeEnabled()
  })
})

describe('confirmação de e-mail', () => {
  // O token vai no fragmento do link: o navegador não o manda ao servidor,
  // e a tela o tira da barra antes de qualquer coisa. Sabotagens previstas:
  // ler o token de ?token=, não limpar a barra, trocar o fragmento por
  // ?token= na limpeza, e voltar a confirmar sozinho na carga.
  it('o link lê o token do fragmento, limpa a barra e pede a senha sem confirmar sozinho', async () => {
    const { falsa, router } = await renderizarPainel('/login', { logado: false, navegador: true })
    await screen.findByRole('heading', { name: 'Entrar no StillOn' })
    falsa.estado.emailConfirmado = false
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    const trocas = vi.spyOn(window.history, 'replaceState')
    await act(() => router.navigate('/confirm-email#token=token-da-caixa'))
    expect(await screen.findByRole('heading', { name: 'Confirme seu e-mail' })).toBeInTheDocument()
    const senha = screen.getByLabelText('Senha')
    expect(senha).toHaveAttribute('type', 'password')
    expect(senha).toHaveAttribute('autocomplete', 'current-password')
    expect(screen.getByRole('main')).toHaveTextContent(
      'Digite a senha que você escolheu no cadastro. Ela garante que só quem criou a conta consegue ativá-la.',
    )
    expect(falsa.chamadas('POST /auth/email/confirm')).toBe(0)
    expect(falsa.estado.emailConfirmado).toBe(false)
    const destinos = trocas.mock.calls.map((c) => String(c[2]))
    expect(destinos.length).toBeGreaterThan(0)
    expect(destinos.every((d) => !d.includes('token-da-caixa'))).toBe(true)
    expect(window.location.href).not.toContain('token-da-caixa')
    expect(window.location.pathname).toBe('/confirm-email')
    expect(urls.filter((u) => u.includes('token-da-caixa'))).toEqual([])
  })

  it('confirmar com a senha certa mostra e-mail confirmado', async () => {
    const { falsa, usuario } = await renderizarPainel('/confirm-email#token=token-da-caixa', { logado: false })
    falsa.estado.emailConfirmado = false
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    await usuario.type(await screen.findByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    expect(await screen.findByRole('heading', { name: 'E-mail confirmado' })).toBeInTheDocument()
    expect(screen.getByText('Sua conta está pronta para criar monitores.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Ir para o painel' })).toHaveAttribute('href', '/')
    expect(falsa.estado.emailConfirmado).toBe(true)
  })

  it('confirmar envia o token e a senha no corpo, sem nada na URL', async () => {
    const { falsa, usuario } = await renderizarPainel('/confirm-email#token=token-da-caixa', { logado: false })
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    await usuario.type(await screen.findByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    await screen.findByRole('heading', { name: 'E-mail confirmado' })
    expect(falsa.corpos('POST /auth/email/confirm')).toEqual([{ token: 'token-da-caixa', password: SENHA }])
    expect(urls.filter((u) => u.includes('token-da-caixa') || u.includes(encodeURIComponent(SENHA)))).toEqual([])
  })

  it('senha errada mantém o formulário com o erro no campo, o foco nele e o link valendo', async () => {
    const { falsa, usuario } = await renderizarPainel('/confirm-email#token=token-da-caixa', { logado: false })
    falsa.estado.emailConfirmado = false
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    const senha = await screen.findByLabelText('Senha')
    await usuario.type(senha, 'outra senha qualquer')
    await usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    await waitFor(() => expect(senha).toHaveAttribute('aria-invalid', 'true'))
    expect(senha).toHaveAccessibleDescription(
      expect.stringContaining('Senha incorreta. O link continua valendo: tente de novo.'),
    )
    expect(senha).toHaveFocus()
    expect(screen.getByRole('heading', { name: 'Confirme seu e-mail' })).toBeInTheDocument()
    expect(falsa.estado.cadastro.tokens.get('token-da-caixa')).toBe('confirm_email')

    // O mesmo link aceita a senha certa depois.
    await usuario.clear(senha)
    await usuario.type(senha, SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    expect(await screen.findByRole('heading', { name: 'E-mail confirmado' })).toBeInTheDocument()
  })

  it('confirmar com token_invalid mostra link inválido ou vencido com caminho para reenviar', async () => {
    const r = await renderizarPainel('/confirm-email#token=usado-ou-vencido', { logado: false })
    await r.usuario.type(await screen.findByLabelText('Senha'), SENHA)
    await r.usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    expect(await screen.findByRole('heading', { name: 'Link inválido ou vencido' })).toBeInTheDocument()
    expect(r.falsa.corpos('POST /auth/email/confirm')).toEqual([{ token: 'usado-ou-vencido', password: SENHA }])
    expect(screen.getByRole('main')).toHaveTextContent(
      'O link de confirmação vale 24 horas e só funciona uma vez. Entre no StillOn e peça outro pelo aviso no topo do painel.',
    )
    expect(screen.getByRole('link', { name: 'Entrar' })).toHaveAttribute('href', '/login')
    r.tela.unmount()

    // Sem token na URL: o mesmo estado, sem formulário, e nada vai ao servidor.
    const sem = await renderizarPainel('/confirm-email', { logado: false })
    expect(await screen.findByRole('heading', { name: 'Link inválido ou vencido' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Senha')).not.toBeInTheDocument()
    expect(sem.falsa.chamadas('POST /auth/email/confirm')).toBe(0)
  })

  it('muitas tentativas pede para aguardar e mantém o formulário', async () => {
    const { falsa, usuario } = await renderizarPainel('/confirm-email#token=token-da-caixa', { logado: false })
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    falsa.estado.forcar.set('POST /auth/email/confirm', { status: 429, code: 'rate_limited' })
    const senha = await screen.findByLabelText('Senha')
    await usuario.type(senha, SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Aguarde um minuto e tente de novo.')
    expect(screen.getByRole('heading', { name: 'Confirme seu e-mail' })).toBeInTheDocument()
    expect(senha).toHaveValue(SENHA)
    expect(screen.getByRole('button', { name: 'Confirmar e-mail' })).toBeEnabled()
  })

  // Conta travada: o backend manda o Retry-After com o que falta da trava
  // (até 900 s). Sabotagem prevista: ignorar o cabeçalho e cair no "um minuto".
  it('conta travada diz quanto tempo falta pelo Retry-After', async () => {
    const { falsa, usuario } = await renderizarPainel('/confirm-email#token=token-da-caixa', { logado: false })
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    servidor.use(
      http.post('*/api/v1/auth/email/confirm', () =>
        HttpResponse.json(
          { error: { code: 'rate_limited', message: 'limite' }, trace_id: 't' },
          { status: 429, headers: { 'Retry-After': '900' } },
        ),
      ),
    )
    const senha = await screen.findByLabelText('Senha')
    await usuario.type(senha, SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Confirmar e-mail' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Aguarde 15 minutos e tente de novo.')
    expect(senha).toHaveValue(SENHA)
  })
})

describe('esqueci a senha', () => {
  // Anti-enumeração: o texto depende só do fluxo, nunca de a conta existir.
  // A API falsa prova que os dois ramos aconteceram (um token só para a
  // conta que existe). Sabotagem prevista: um trecho do texto depender do
  // e-mail digitado.
  it('esqueci mostra o mesmo texto com ou sem conta', async () => {
    const { usuario, falsa } = await renderizarPainel('/forgot-password', { logado: false })
    await screen.findByRole('heading', { name: 'Redefinir a senha' })
    const textoDoCartao = (email: string) =>
      (screen.getByRole('main').textContent ?? '').replaceAll(email, '{email}')

    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await turnstile.resolver()
    expect(turnstile.render.mock.calls[0]?.[1]).toMatchObject({ action: 'forgot' })
    await usuario.click(screen.getByRole('button', { name: 'Enviar link' }))
    await screen.findByRole('heading', { name: 'Confira seu e-mail' })
    const comConta = textoDoCartao(EMAIL)
    expect(comConta).toContain('Abra o link em até 1 hora para escolher uma senha nova.')
    expect(falsa.estado.cadastro.tokens.size).toBe(1)

    // "Pedir de novo" volta ao formulário com o e-mail e um widget novo.
    await usuario.click(screen.getByRole('button', { name: 'Pedir de novo' }))
    const email = await screen.findByLabelText('E-mail')
    expect(email).toHaveValue(EMAIL)
    expect(screen.getByRole('button', { name: 'Enviar link' })).toBeDisabled()
    await usuario.clear(email)
    await usuario.type(email, 'ninguem@exemplo.com')
    await waitFor(() => expect(turnstile.render).toHaveBeenCalledTimes(2))
    await turnstile.resolver()
    await usuario.click(screen.getByRole('button', { name: 'Enviar link' }))
    await screen.findByRole('heading', { name: 'Confira seu e-mail' })
    expect(textoDoCartao('ninguem@exemplo.com')).toBe(comConta)
    expect(falsa.estado.cadastro.tokens.size).toBe(1)
  })
})

describe('redefinição de senha', () => {
  it('redefinir envia new_password e leva ao login com aviso de senha trocada', async () => {
    const { falsa, router, usuario } = await renderizarPainel('/login', { logado: false, navegador: true })
    await screen.findByRole('heading', { name: 'Entrar no StillOn' })
    falsa.estado.cadastro.tokens.set('token-de-reset', 'reset_password')
    await act(() => router.navigate('/reset-password#token=token-de-reset'))
    await screen.findByRole('heading', { name: 'Escolha uma senha nova' })
    expect(window.location.href).not.toContain('token-de-reset')
    await usuario.type(screen.getByLabelText('Senha nova'), SENHA_NOVA)
    await usuario.click(screen.getByRole('button', { name: 'Salvar senha' }))
    await screen.findByRole('heading', { name: 'Entrar no StillOn' })
    expect(screen.getByText('Senha redefinida. Entre com a senha nova.')).toBeInTheDocument()
    expect(falsa.corpos('POST /auth/password/reset')).toEqual([{ token: 'token-de-reset', new_password: SENHA_NOVA }])
    expect(urls.filter((u) => u.includes('token-de-reset'))).toEqual([])
  })

  it('preserva a confirmação quando redefinir revoga a sessão aberta da própria conta', async () => {
    const { falsa, sessao, api, cliente, usuario } = await renderizarPainel('/reset-password#token=token-de-reset')
    cliente.setQueryData(chaves.me, await api.me())
    servidor.use(http.post('*/api/v1/auth/password/reset', () => {
      // Mesmo com /me em cache e o token de acesso no prazo, o servidor
      // recusa a sessão revogada na primeira chamada autenticada.
      falsa.estado.familiaRevogada = true
      return new HttpResponse(null, { status: 204 })
    }))
    await usuario.type(await screen.findByLabelText('Senha nova'), SENHA_NOVA)
    await usuario.click(screen.getByRole('button', { name: 'Salvar senha' }))

    const titulo = await screen.findByRole('heading', { name: 'Entrar no StillOn' })
    expect(titulo).toHaveAccessibleDescription('Senha redefinida. Entre com a senha nova.')
    expect(sessao.estado()).toBe('anonimo')
    expect(falsa.chamadas('GET /me')).toBe(2)
    expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
  })

  it.each(['outra conta', 'sessão ainda incerta'] as const)(
    'preserva a confirmação sem encerrar a sessão aberta (%s)',
    async (caso) => {
      const { falsa, sessao, usuario } = await renderizarPainel('/reset-password#token=token-de-reset')
      servidor.use(http.post('*/api/v1/auth/password/reset', () => {
        if (caso === 'sessão ainda incerta') {
          falsa.estado.forcar.set('GET /me', { status: 503, code: 'unavailable' })
        }
        return new HttpResponse(null, { status: 204 })
      }))
      await usuario.type(await screen.findByLabelText('Senha nova'), SENHA_NOVA)
      await usuario.click(screen.getByRole('button', { name: 'Salvar senha' }))

      const titulo = await screen.findByRole('heading', { name: 'Senha redefinida' })
      expect(titulo).toHaveFocus()
      expect(sessao.estado()).toBe('autenticado')
      expect(screen.queryByRole('button', { name: 'Salvar senha' })).not.toBeInTheDocument()
      expect(falsa.chamadas('GET /me')).toBe(1)
      falsa.estado.forcar.delete('GET /me')
      await usuario.click(screen.getByRole('link', { name: 'Ir para o painel' }))
      expect(await screen.findByRole('heading', { name: 'Painel' })).toBeInTheDocument()
      expect(falsa.chamadas('POST /auth/logout')).toBe(0)
    },
  )

  it('redefinir com token_invalid mostra link inválido com caminho para pedir outro', async () => {
    const { usuario } = await renderizarPainel('/reset-password#token=vencido', { logado: false })
    await usuario.type(await screen.findByLabelText('Senha nova'), SENHA_NOVA)
    await usuario.click(screen.getByRole('button', { name: 'Salvar senha' }))
    expect(await screen.findByRole('heading', { name: 'Link inválido ou vencido' })).toBeInTheDocument()
    expect(screen.getByRole('main')).toHaveTextContent('O link de redefinição vale 1 hora e só funciona uma vez. Peça outro.')
    expect(screen.getByRole('link', { name: 'Pedir outro link' })).toHaveAttribute('href', '/forgot-password')
  })
})

describe('envio dos formulários públicos', () => {
  it.each([
    {
      rota: '/signup',
      endpoint: '/auth/signup',
      acao: 'Criar conta',
      enviando: 'Criando conta…',
      campos: [{ rotulo: 'E-mail', valor: NOVO }, { rotulo: 'Senha', valor: SENHA_NOVA }],
      desafio: true,
      via: 'enter',
      mostrarSenha: true,
    },
    {
      rota: '/forgot-password',
      endpoint: '/auth/password/forgot',
      acao: 'Enviar link',
      enviando: 'Enviando…',
      campos: [{ rotulo: 'E-mail', valor: NOVO }],
      desafio: true,
      via: 'botão',
      mostrarSenha: false,
    },
    {
      rota: '/reset-password#token=token-de-reset',
      endpoint: '/auth/password/reset',
      acao: 'Salvar senha',
      enviando: 'Salvando…',
      campos: [{ rotulo: 'Senha nova', valor: SENHA_NOVA }],
      desafio: false,
      via: 'enter',
      mostrarSenha: true,
      moverFocoPara: 'Escolha uma senha nova',
    },
    {
      rota: '/confirm-email#token=token-da-caixa',
      endpoint: '/auth/email/confirm',
      acao: 'Confirmar e-mail',
      enviando: 'Confirmando…',
      campos: [{ rotulo: 'Senha', valor: SENHA }],
      desafio: false,
      via: 'enter',
      mostrarSenha: false,
    },
  ])('$rota bloqueia os controles enquanto envia e recupera os valores e o foco na falha', async ({
    rota, endpoint, acao, enviando, campos, desafio, via, mostrarSenha, moverFocoPara,
  }) => {
    let liberar!: () => void
    const resposta = new Promise<void>((resolve) => { liberar = resolve })
    const pedidos = () => urls.filter((url) => new URL(url).pathname === `/api/v1${endpoint}`)
    const { usuario } = await renderizarPainel(rota, { logado: false })
    servidor.use(http.post(`*/api/v1${endpoint}`, async () => {
      await resposta
      return HttpResponse.json(
        { error: { code: 'unavailable', message: 'indisponível' }, trace_id: 't' },
        { status: 503 },
      )
    }))
    const preenchidos = []
    for (const { rotulo, valor } of campos) {
      const entrada = await screen.findByLabelText(rotulo)
      await usuario.type(entrada, valor)
      preenchidos.push({ entrada, valor })
    }
    if (desafio) await turnstile.resolver()

    const botao = screen.getByRole('button', { name: acao })
    const botoesDeSenha = screen.queryAllByRole('button', { name: 'Mostrar senha' })
    expect(botoesDeSenha).toHaveLength(mostrarSenha ? 1 : 0)
    const primeiroCampo = preenchidos[0]!.entrada
    const origem = via === 'enter' ? primeiroCampo : botao
    const focoExterno = moverFocoPara ? screen.getByRole('heading', { name: moverFocoPara }) : null

    try {
      await usuario.click(origem)
      if (via === 'enter') await usuario.keyboard('{Enter}')
      await waitFor(() => expect(pedidos()).toHaveLength(1))
      expect(botao).toHaveAccessibleName(enviando)
      expect(botao).toBeDisabled()
      for (const { entrada, valor } of preenchidos) {
        expect(entrada).toBeVisible()
        expect(entrada).toHaveValue(valor)
        expect(entrada).toBeDisabled()
      }
      for (const mostrar of botoesDeSenha) expect(mostrar).toBeDisabled()
      expect(turnstile.render).toHaveBeenCalledTimes(desafio ? 1 : 0)

      // O jsdom pode manter o foco num controle recém-desabilitado.
      origem.blur()
      focoExterno?.focus()
      fireEvent.submit(botao.closest('form')!)
    } finally {
      liberar()
    }

    expect(await screen.findByRole('alert')).toHaveTextContent('Serviço indisponível no momento.')
    for (const { entrada, valor } of preenchidos) {
      expect(entrada).toHaveValue(valor)
      expect(entrada).toBeEnabled()
    }
    for (const mostrar of botoesDeSenha) expect(mostrar).toBeEnabled()
    expect(focoExterno ?? primeiroCampo).toHaveFocus()
    expect(botao.matches(':disabled')).toBe(desafio)
    if (desafio) await turnstile.resolver()
    expect(botao).toHaveAccessibleName(acao)
    expect(botao).toBeEnabled()
    expect(pedidos()).toHaveLength(1)
  })
})

describe('cadastro desligado', () => {
  // Sabotagem prevista: ignorar o enabled da config.
  it('as telas públicas com cadastro desligado mostram que o cadastro está fechado', async () => {
    const { falsa, router, cliente } = await renderizarPainel('/login', { logado: false })
    await screen.findByRole('heading', { name: 'Entrar no StillOn' })
    falsa.estado.cadastro.habilitado = false
    cliente.clear()

    await act(() => router.navigate('/signup'))
    expect(await screen.findByRole('heading', { name: 'Cadastro fechado' })).toBeInTheDocument()
    const main = screen.getByRole('main')
    expect(main).toHaveTextContent('Este servidor não aceita contas novas no momento.')
    expect(within(main).getByRole('link', { name: 'Entrar' })).toHaveAttribute('href', '/login')
    expect(screen.queryByLabelText('E-mail')).not.toBeInTheDocument()

    await act(() => router.navigate('/forgot-password'))
    await screen.findByText(
      'A redefinição por e-mail não está ativa neste servidor. Fale com quem administra o StillOn.',
    )
    expect(screen.getByRole('heading', { name: 'Redefinir a senha' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Voltar para entrar' })).toHaveAttribute('href', '/login')
    expect(screen.queryByLabelText('E-mail')).not.toBeInTheDocument()
    expect(turnstile.render).not.toHaveBeenCalled()
  })
})
