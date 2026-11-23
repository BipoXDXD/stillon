import { act, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { EMAIL, SITE_KEY_DO_TURNSTILE, TOKEN_DO_TURNSTILE } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'
import type { OpcoesDoTurnstile } from './Turnstile'

// As telas públicas do cadastro contra a API falsa, com um window.turnstile
// de mentira: o teste faz o papel da Cloudflare chamando os callbacks.
function instalarTurnstile() {
  const widgets: OpcoesDoTurnstile[] = []
  const falso = {
    render: vi.fn<(alvo: HTMLElement, o: OpcoesDoTurnstile) => string>((_alvo, o) => {
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

describe('confirmação de e-mail', () => {
  // O token vai no fragmento do link: o navegador não o manda ao servidor,
  // e a tela o tira da barra antes de qualquer coisa. Três sabotagens
  // previstas: ler o token de ?token= (sem token, nada de POST), não limpar a
  // barra, e trocar o fragmento por ?token= na limpeza.
  it('confirmar lê o token do fragmento, limpa a barra e confirma', async () => {
    const { falsa, router } = await renderizarPainel('/login', { logado: false, navegador: true })
    await screen.findByRole('heading', { name: 'Entrar no StillOn' })
    falsa.estado.emailConfirmado = false
    falsa.estado.cadastro.tokens.set('token-da-caixa', 'confirm_email')
    const trocas = vi.spyOn(window.history, 'replaceState')
    await act(() => router.navigate('/confirm-email#token=token-da-caixa'))
    expect(await screen.findByRole('heading', { name: 'E-mail confirmado' })).toBeInTheDocument()
    expect(screen.getByText('Sua conta está pronta para criar monitores.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Ir para o painel' })).toHaveAttribute('href', '/')
    // Um POST só (o StrictMode roda o efeito duas vezes; o token é de uso único).
    expect(falsa.corpos('POST /auth/email/confirm')).toEqual([{ token: 'token-da-caixa' }])
    expect(falsa.estado.emailConfirmado).toBe(true)
    const destinos = trocas.mock.calls.map((c) => String(c[2]))
    expect(destinos.length).toBeGreaterThan(0)
    expect(destinos.every((d) => !d.includes('token-da-caixa'))).toBe(true)
    expect(window.location.href).not.toContain('token-da-caixa')
    expect(window.location.pathname).toBe('/confirm-email')
    expect(urls.filter((u) => u.includes('token-da-caixa'))).toEqual([])
  })

  it('confirmar com token_invalid mostra link inválido ou vencido com caminho para reenviar', async () => {
    const r = await renderizarPainel('/confirm-email#token=usado-ou-vencido', { logado: false })
    expect(await screen.findByRole('heading', { name: 'Link inválido ou vencido' })).toBeInTheDocument()
    expect(r.falsa.corpos('POST /auth/email/confirm')).toEqual([{ token: 'usado-ou-vencido' }])
    expect(screen.getByRole('main')).toHaveTextContent(
      'O link de confirmação vale 24 horas e só funciona uma vez. Entre no StillOn e peça outro pelo aviso no topo do painel.',
    )
    expect(screen.getByRole('link', { name: 'Entrar' })).toHaveAttribute('href', '/login')
    r.tela.unmount()

    // Sem token na URL: o mesmo estado, e nada vai ao servidor.
    const sem = await renderizarPainel('/confirm-email', { logado: false })
    expect(await screen.findByRole('heading', { name: 'Link inválido ou vencido' })).toBeInTheDocument()
    expect(sem.falsa.chamadas('POST /auth/email/confirm')).toBe(0)
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

  it('redefinir com token_invalid mostra link inválido com caminho para pedir outro', async () => {
    const { usuario } = await renderizarPainel('/reset-password#token=vencido', { logado: false })
    await usuario.type(await screen.findByLabelText('Senha nova'), SENHA_NOVA)
    await usuario.click(screen.getByRole('button', { name: 'Salvar senha' }))
    expect(await screen.findByRole('heading', { name: 'Link inválido ou vencido' })).toBeInTheDocument()
    expect(screen.getByRole('main')).toHaveTextContent('O link de redefinição vale 1 hora e só funciona uma vez. Peça outro.')
    expect(screen.getByRole('link', { name: 'Pedir outro link' })).toHaveAttribute('href', '/forgot-password')
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
