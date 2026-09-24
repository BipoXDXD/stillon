import { act, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it } from 'vitest'
import { RECUPERACAO_OK, TOTP_OK } from '@/test/api-falsa'
import { novaSessao } from '@/test/ambiente'
import { servidor } from '@/test/setup'
import { EMAIL, renderizarPainel, SENHA } from '@/test/renderizar'
import { destinoSeguro } from './PaginaLogin'

describe('destinoSeguro', () => {
  // Duas regras, duas sabotagens previstas: trocar a comparação de origem
  // por "if (Math.random() > 2)" → "//evil.example" passa; tirar a
  // faixa de controle da regex (deixar só "/[\\]/") → "/alvos\u0007" passa.
  it('só segue caminho do painel', () => {
    expect(destinoSeguro('/targets/x?janela=7d')).toBe('/targets/x?janela=7d')
    const ruins = [
      '//evil.example',
      '/\\evil.example',
      '/\t/evil.example',
      '/\n/evil.example',
      '/alvos\u0007',
      'https://evil.example',
      'javascript:alert(1)',
      '',
      null,
    ]
    for (const ruim of ruins) expect({ ruim, destino: destinoSeguro(ruim) }).toEqual({ ruim, destino: '/' })
  })
})

// I3 da revisão: fora de contexto seguro não há navigator.locks, e duas abas
// derrubam a sessão. O painel avisa, sem trava substituta. Sabotagem
// prevista: em AvisoDeContexto, trocar "|| window.isSecureContext" por
// "|| !window.isSecureContext".
describe('contexto seguro', () => {
  afterEach(() => {
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
  })

  it('sem HTTPS (nem localhost), avisa na tela de entrada e no painel', async () => {
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    const r = await renderizarPainel('/login', { logado: false })
    expect(await screen.findByRole('alert')).toHaveTextContent('Este painel precisa de HTTPS')
    r.tela.unmount()
    await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    expect(screen.getByRole('alert')).toHaveTextContent('com mais de uma aba aberta, a sessão cai')
  })

  // O aviso também aparece antes de o painel abrir: no splash do boot e na
  // tela de servidor fora do ar. Sabotagem prevista: tirar o
  // <AvisoDeContexto /> de um dos dois ramos de ExigirSessao.
  it('sem HTTPS, avisa também no splash do boot e na tela de servidor fora do ar', async () => {
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    const r = await renderizarPainel('/', { atrasoDoBoot: 200 })
    expect(screen.getByText('Carregando o Vigilume…')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('Este painel precisa de HTTPS')
    await screen.findByRole('heading', { name: 'Painel' })
    r.tela.unmount()

    // Offline: sem sessão, o cookie volta a ser tentado com a rede caída.
    const { sessao, router } = await renderizarPainel('/login', { logado: false })
    await screen.findByRole('heading', { name: 'Entrar no Vigilume' })
    servidor.use(http.post('*/api/v1/auth/refresh', () => HttpResponse.error()))
    await act(() => sessao.tentarDeNovo())
    expect(sessao.estado()).toBe('offline')
    await act(() => router.navigate('/'))
    await screen.findByRole('heading', { name: 'Não foi possível falar com o servidor' })
    const alertas = screen.getAllByRole('alert')
    expect(alertas).toHaveLength(2)
    expect(alertas[0]).toHaveTextContent('Este painel precisa de HTTPS')
  })

  it('com contexto seguro, nada de aviso', async () => {
    await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('entrar', () => {
  it('sem sessão, a rota protegida leva ao login guardando o destino', async () => {
    const { router } = await renderizarPainel('/incidents', { logado: false })
    expect(await screen.findByRole('heading', { name: 'Entrar no Vigilume' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(router.state.location.search).toBe('?next=%2Fincidents')
  })

  // Spec §11.2, item 1: enquanto o boot verifica o cookie, um splash — nunca
  // o login, que piscaria a cada F5. Sabotagem prevista: tirar o ramo
  // "verificando" de ExigirSessao.
  it('durante o boot, mostra o splash, e não o login nem o painel', async () => {
    await renderizarPainel('/', { atrasoDoBoot: 200 })
    expect(screen.getByText('Carregando o Vigilume…')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Entrar no Vigilume' })).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: 'Painel' })).toBeInTheDocument()
  })

  // A árvore dos testes roda sob StrictMode, como a de produção (spec §7.2).
  // D9: o boot fica fora da árvore (main.tsx), e a carga inteira renova UMA
  // vez. Sabotagem prevista, a do boot dentro de efeito sem guarda: em
  // ExigirSessao, "useEffect(() => void sessao.iniciar(), [sessao])" MAIS a
  // sabotagem 2 da Task 2 (as duas guardas de sessao.ts) → 3 refreshes com o
  // mesmo cookie. Só o efeito, com as guardas, fica verde: elas existem para isso.
  it('com o cookie, o F5 entra direto, sem passar pelo login, com um refresh só', async () => {
    const { router, falsa } = await renderizarPainel('/')
    await screen.findByRole('heading', { name: /^(Painel|Entrar no Vigilume)$/ })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(screen.getByRole('heading', { name: 'Painel' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/')
  })

  it('senha certa leva ao destino guardado', async () => {
    const { usuario, router } = await renderizarPainel('/login?next=%2Fsem-tela%3Fx%3D1', { logado: false })
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    expect(await screen.findByRole('heading', { name: 'Página não encontrada' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/sem-tela')
    expect(router.state.location.search).toBe('?x=1')
  })

  // R34 do Plano 1: o bloqueio não se anuncia. A tela mostra sempre o mesmo
  // texto, e o campo de senha é limpo.
  it('senha errada mostra o texto genérico', async () => {
    const { usuario } = await renderizarPainel('/login', { logado: false })
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), 'errada')
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('E-mail ou senha incorretos.')
    expect(screen.getByLabelText('Senha')).toHaveValue('')
  })

  it('com 2FA, pede o código; aceita também o de recuperação', async () => {
    for (const codigo of [TOTP_OK, RECUPERACAO_OK]) {
      const { usuario, falsa, tela } = await renderizarPainel('/login', { logado: false })
      falsa.estado.totpLigado = true
      await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
      await usuario.type(screen.getByLabelText('Senha'), SENHA)
      await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
      if (codigo === RECUPERACAO_OK) {
        await usuario.click(await screen.findByRole('button', { name: 'Usar um código de recuperação' }))
        await usuario.type(screen.getByLabelText('Código de recuperação'), codigo)
      } else {
        await usuario.type(await screen.findByLabelText('Código do aplicativo autenticador'), codigo)
      }
      await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
      expect(await screen.findByRole('heading', { name: 'Painel' })).toBeInTheDocument()
      tela.unmount()
    }
  })

  it('código errado diz "Código incorreto" e continua no passo do código', async () => {
    const { usuario, falsa } = await renderizarPainel('/login', { logado: false })
    falsa.estado.totpLigado = true
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    await usuario.type(await screen.findByLabelText('Código do aplicativo autenticador'), '000000')
    await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Código incorreto.')
    expect(screen.getByLabelText('Código do aplicativo autenticador')).toBeInTheDocument()
  })

  // Task 11 do Plano 5: a senha trocada (em outro aparelho) depois do
  // desafio faz o verify responder invalid_credentials, também com o código
  // certo. A tela sugere voltar e entrar de novo. Sabotagem prevista: tirar
  // a segunda frase da mensagem de invalid_credentials do verificar().
  it('código certo recusado porque a senha mudou depois do desafio: sugere entrar de novo', async () => {
    const { usuario, falsa } = await renderizarPainel('/login', { logado: false })
    const outra = novaSessao()
    await outra.sessao.entrar(EMAIL, SENHA)
    falsa.estado.totpLigado = true
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    await screen.findByLabelText('Código do aplicativo autenticador')
    await outra.api.trocarSenha({
      current_password: SENHA,
      new_password: 'outra senha bem comprida',
      totp_code: TOTP_OK,
    })
    await usuario.type(screen.getByLabelText('Código do aplicativo autenticador'), TOTP_OK)
    await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Se a senha mudou há pouco, volte e entre de novo.')
    expect(falsa.chamadas('POST /auth/totp/verify')).toBe(1)

    // A mensagem manda voltar, e o passo do código tem como. Sabotagem
    // prevista: tirar o botão Voltar do formulário do código.
    await usuario.click(screen.getByRole('button', { name: 'Voltar' }))
    expect(screen.queryByLabelText('Código do aplicativo autenticador')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByLabelText('E-mail')).toHaveValue(EMAIL)
    expect(screen.getByLabelText('Senha')).toHaveValue('')
    await waitFor(() => expect(screen.getByLabelText('Senha')).toHaveFocus())
    // Entrar de novo abre um desafio novo, que o código aceita. (A API falsa
    // não guarda a senha nova: o login continua com SENHA.)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    await usuario.type(await screen.findByLabelText('Código do aplicativo autenticador'), TOTP_OK)
    await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
    expect(await screen.findByRole('heading', { name: 'Painel' })).toBeInTheDocument()
  })

  // O desafio vence (5 minutos): a tela volta ao passo da senha com o foco no
  // campo da senha (o botão que tinha o foco sumiu), e o próximo desafio
  // começa no código do aplicativo. Sabotagens previstas: tirar o
  // "autoFocus={voltou}" (o foco cai no body) e o "setRecuperacao(false)".
  it('desafio vencido volta ao passo da senha, com o foco na senha', async () => {
    const { usuario, falsa } = await renderizarPainel('/login', { logado: false })
    falsa.estado.totpLigado = true
    await usuario.type(await screen.findByLabelText('E-mail'), EMAIL)
    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    await usuario.click(await screen.findByRole('button', { name: 'Usar um código de recuperação' }))
    servidor.use(
      http.post('*/api/v1/auth/totp/verify', () =>
        HttpResponse.json({ error: { code: 'unauthorized', message: 'desafio vencido' } }, { status: 401 }),
      ),
    )
    await usuario.type(screen.getByLabelText('Código de recuperação'), RECUPERACAO_OK)
    await usuario.click(screen.getByRole('button', { name: 'Verificar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('O tempo para digitar o código acabou. Entre de novo.')
    expect(screen.getByLabelText('Senha')).toHaveValue('')
    await waitFor(() => expect(screen.getByLabelText('Senha')).toHaveFocus())

    await usuario.type(screen.getByLabelText('Senha'), SENHA)
    await usuario.click(screen.getByRole('button', { name: 'Entrar' }))
    expect(await screen.findByLabelText('Código do aplicativo autenticador')).toBeInTheDocument()
  })
})

describe('sair', () => {
  it('Sair volta ao login e o cache da sessão some', async () => {
    const { usuario, cliente, router } = await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    expect(cliente.getQueryCache().getAll().length).toBeGreaterThan(0)
    await usuario.click(screen.getByRole('button', { name: 'Sair' }))
    await screen.findByRole('heading', { name: 'Entrar no Vigilume' })
    expect(router.state.location.pathname).toBe('/login')
    await waitFor(() => expect(cliente.getQueryCache().getAll()).toEqual([]))
  })

  it('sessão encerrada em outra aba volta ao login com o aviso', async () => {
    const { sessao } = await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    sessao.encerrar('outra-aba')
    expect(await screen.findByText('Você saiu em outra aba.')).toBeInTheDocument()
  })
})

// Spec §4.9 e §3.7 juntos: na carga da página, o primeiro Tab é o "Pular para
// o conteúdo"; depois de uma navegação dentro do painel, o foco vai ao <h1>
// da tela nova. Sabotagens previstas, uma por metade: em Titulo, trocar
// "focar.current = !primeiraTela" por "focar.current = !primeiraTela || true"
// (a carga foca o título, e o primeiro Tab sai dele); e trocar
// "if (focar.current)" por "if (focar.current && Math.random() > 2)" (a
// navegação não foca).
describe('foco', () => {
  it('na carga da página, o primeiro Tab é o "Pular para o conteúdo"', async () => {
    const r = await renderizarPainel('/')
    const titulo = await screen.findByRole('heading', { name: 'Painel' })
    expect(titulo).not.toHaveFocus()
    await r.usuario.tab()
    expect(screen.getByRole('link', { name: 'Pular para o conteúdo' })).toHaveFocus()
  })

  it('depois de navegar dentro do painel, o foco vai ao título da tela nova', async () => {
    const r = await renderizarPainel('/')
    await screen.findByRole('heading', { name: 'Painel' })
    // Uma tela que existe desde esta tarefa (D12): a de página não encontrada.
    await r.router.navigate('/sem-tela')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Página não encontrada', level: 1 })).toHaveFocus())
  })
})
