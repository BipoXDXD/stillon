import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { RECUPERACAO_OK, TOTP_OK } from '@/test/api-falsa'
import { sessao as familia } from '@/test/fixtures'
import { renderizarPainel, SENHA } from '@/test/renderizar'
import { servidor } from '@/test/setup'

describe('senha', () => {
  it('a política conta code points: 14 emojis não bastam, mesmo sendo 28 unidades UTF-16', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.type(await screen.findByLabelText('Senha atual'), SENHA)
    const nova = screen.getByLabelText('Senha nova')
    await r.usuario.type(nova, '😀'.repeat(14))
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), '😀'.repeat(14))
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    await waitFor(() => expect(nova).toHaveAccessibleDescription(/Mínimo de 15 caracteres/))
    expect(r.falsa.chamadas('POST /auth/password')).toBe(0)
  })

  it('troca e avisa da janela de 15 minutos', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.type(await screen.findByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByText(/pode durar até 15 minutos/)).toBeInTheDocument()
    expect(r.falsa.corpos('POST /auth/password')[0]).toEqual({
      current_password: SENHA,
      new_password: 'outra senha bem comprida',
    })
  })

  it('com 2FA, pede o código; senha ou código errados dão um texto só', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Código do autenticador'), '000000')
    await r.usuario.type(screen.getByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    const alerta = await screen.findByRole('alert')
    expect(alerta).toHaveTextContent('Senha atual ou código incorretos.')
    // Um código de 6 dígitos não é de recuperação: nada de aviso de código gasto.
    expect(alerta).not.toHaveTextContent('código de recuperação')
  })

  // PENDENCIAS do Plano 6: o código de recuperação é gasto antes da
  // transação da troca; se ela falha depois, o usuário tem de saber que
  // aquele código não vale mais. Sabotagem prevista: em ehDeRecuperacao,
  // trocar "!/^\d{6}$/.test(codigo)" por "/^\d{6}$/.test(codigo)" (este fica
  // vermelho, e o teste acima também).
  it('troca que falha depois de gastar o código de recuperação avisa que ele pode ter sido consumido', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    r.falsa.estado.trocaFalhaDepoisDoCodigo = true
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Código do autenticador'), RECUPERACAO_OK)
    await r.usuario.type(screen.getByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Se você usou um código de recuperação, ele pode ter sido consumido; use outro.',
    )
    expect(r.falsa.estado.recuperacaoGasta.has(RECUPERACAO_OK)).toBe(true)
  })

  // D2 e I5 da revisão: /auth/* nunca renova sozinho. O formulário fica, e
  // "Renovar acesso e tentar de novo" renova; quem reenvia é o usuário.
  // Sabotagens previstas: a 7 da Task 2 (renovar em /auth/*: a troca passa
  // no primeiro envio, sem o aviso) e, no botão, trocar
  // "const novo = await sessao.renovar(sessao.token())" por
  // "const novo = sessao.token()" (não renova: o segundo envio volta 401).
  it('com o acesso vencido nesta tela, guarda o formulário e oferece renovar; o reenvio é do usuário', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.type(await screen.findByLabelText('Senha atual'), SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    const refreshes = r.falsa.chamadas('POST /auth/refresh')
    r.falsa.expirarAcessos()
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('O acesso desta tela venceu.')
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(refreshes)
    expect(screen.getByLabelText('Senha atual')).toHaveValue(SENHA)
    await r.usuario.click(screen.getByRole('button', { name: 'Renovar acesso e tentar de novo' }))
    expect(await screen.findByText('Acesso renovado. Envie de novo.')).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(refreshes + 1)
    expect(r.falsa.chamadas('POST /auth/password')).toBe(1)
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByText(/pode durar até 15 minutos/)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/password')).toBe(2)
    // O aviso de renovado não sobrevive ao envio seguinte. Sabotagem
    // prevista: no início do envio, trocar "setErro(null)" por
    // "setErro((e) => (e?.renovado ? e : null))".
    expect(screen.queryByText('Acesso renovado. Envie de novo.')).not.toBeInTheDocument()
  })

  // Revisão do plano: a falha de renovar (rede caída) é do erro em que o
  // botão foi clicado; o erro seguinte mostra o próprio texto, não a falha
  // velha. Sabotagem: tirar o "setFalha(null)" que acompanha a troca de erro.
  it('a falha de renovar não esconde o texto do erro seguinte', async () => {
    const r = await renderizarPainel('/settings')
    const atual = await screen.findByLabelText('Senha atual')
    await r.usuario.type(atual, SENHA)
    await r.usuario.type(screen.getByLabelText('Senha nova'), 'outra senha bem comprida')
    await r.usuario.type(screen.getByLabelText('Confirme a senha nova'), 'outra senha bem comprida')
    r.falsa.expirarAcessos()
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('O acesso desta tela venceu.')
    servidor.use(http.post('*/api/v1/auth/refresh', () => HttpResponse.error(), { once: true }))
    await r.usuario.click(screen.getByRole('button', { name: 'Renovar acesso e tentar de novo' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Sem conexão com o servidor.')
    // A rede volta, o acesso é renovado por fora (outra requisição), e o
    // próximo envio erra a senha: o texto é o dele.
    await r.sessao.renovar(r.sessao.token())
    await r.usuario.clear(atual)
    await r.usuario.type(atual, 'senha errada, mas comprida')
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar a senha' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Senha atual incorreta.'))
    expect(screen.getByRole('alert')).not.toHaveTextContent('Sem conexão')
  })

  // Task 11 do Plano 5: os diálogos do 2FA respondem "unauthorized" quando
  // a família da sessão morreu. O mesmo caminho: renovar; refresh recusado
  // é o fim da sessão, com o motivo.
  it('no diálogo do 2FA, acesso vencido com a sessão morta volta ao login', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    // A família morre (encerrada em outro aparelho); o access token desta
    // aba continua válido, e é a guarda da rota que o recusa.
    r.falsa.estado.familiaRevogada = true
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('O acesso desta tela venceu.')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Renovar acesso e tentar de novo' }))
    expect(await screen.findByText('Sua sessão terminou. Entre de novo.')).toBeInTheDocument()
    expect(r.router.state.location.pathname).toBe('/login')
  })
})

describe('sessões', () => {
  // Nota do Plano 5 §12: o 204 já apagou o cookie; o painel larga o token e
  // vai ao login SEM chamar refresh nem logout. Sabotagem prevista: trocar
  // sessao.encerrar('encerrada-aqui') por sessao.sair() → 1 logout.
  it('encerrar a própria sessão volta ao login, sem refresh nem logout depois', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia(), familia({ id: 'fam-2', current: false, user_agent: '' })]
    await r.cliente.refetchQueries()
    const itens = await screen.findAllByRole('button', { name: 'Encerrar' })
    expect(screen.getByText('Esta sessão')).toBeInTheDocument()
    expect(screen.getByText('Navegador desconhecido')).toBeInTheDocument()
    const refreshesAntes = r.falsa.chamadas('POST /auth/refresh')
    await r.usuario.click(itens[0]!)
    const dialogo = await screen.findByRole('alertdialog')
    expect(within(dialogo).getByText('Sair deste navegador?')).toBeInTheDocument()
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Encerrar' }))
    expect(await screen.findByText('Você encerrou esta sessão.')).toBeInTheDocument()
    expect(r.router.state.location.pathname).toBe('/login')
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(refreshesAntes)
    expect(r.falsa.chamadas('POST /auth/logout')).toBe(0)
    expect(r.sessao.token()).toBeNull()
  })

  it('404 ao encerrar é "já tinha terminado", não erro', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia({ id: 'fam-2', current: false })]
    await r.cliente.refetchQueries()
    r.falsa.estado.sessoes = []
    await r.usuario.click(await screen.findByRole('button', { name: 'Encerrar' }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Encerrar' }))
    expect(await screen.findByText('Esta sessão já tinha terminado.')).toBeInTheDocument()
  })

  it('o user agent é texto, nunca HTML', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia({ current: false, user_agent: '<img src=x onerror=alert(1)>' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
    expect(document.querySelector('img')).toBeNull()
  })
})

describe('2FA', () => {
  it('ativar: senha, QR e chave, código, e os códigos de recuperação só fecham depois de guardados', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    expect(await within(dialogo).findByText('JBSWY3DPEHPK3PXP')).toBeInTheDocument()
    expect(within(dialogo).getByTitle('QR code do autenticador')).toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('Código de 6 dígitos'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    expect(await within(dialogo).findByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
    const concluir = within(dialogo).getByRole('button', { name: 'Concluir' })
    expect(concluir).toBeDisabled()
    await r.usuario.click(within(dialogo).getByLabelText('Guardei os códigos'))
    await r.usuario.click(concluir)
    expect(await screen.findByText('Ativada.')).toBeInTheDocument()
  })

  // PENDENCIAS do Plano 6: trocar a senha apaga o cadastro pendente, e o
  // confirm responde o mesmo 400 de um código errado.
  it('código não aceito na ativação sugere recomeçar, se a senha foi trocada', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await within(dialogo).findByLabelText('Código de 6 dígitos')
    // A senha é trocada nesta mesma sessão (outra aba): a API falsa apaga o
    // cadastro pendente, como o servidor.
    await r.api.trocarSenha({ current_password: SENHA, new_password: 'outra senha bem comprida' })
    await r.usuario.type(within(dialogo).getByLabelText('Código de 6 dígitos'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('feche e comece de novo')
  })

  // I2 da revisão: no passo dos códigos, Esc (e o clique fora) não fecham —
  // os códigos não aparecem de novo. Sabotagem prevista: no Dialogo de
  // ativar, trocar "travado={travado}" por "travado={travado && Math.random() > 2}".
  it('no passo dos códigos, Esc não fecha o diálogo', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    await within(dialogo).findByText('AAAABBBBCCCCDDDD')
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
    expect(within(dialogo).getByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
    // O /me relido (foco da janela, outro poll) já diz "ligado": o diálogo
    // continua, pelo travado. Sabotagem prevista: trocar
    // "(totp === false || travado)" por "totp === false && (travado || !travado)".
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    await waitFor(() => expect(r.falsa.chamadas('GET /me')).toBeGreaterThanOrEqual(3))
    expect(screen.getByRole('dialog')).toBe(dialogo)
    expect(within(dialogo).getByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
  })

  // I2/M11: ['me'] não vence sozinho; abrir e fechar os diálogos do 2FA o
  // releem. Sabotagem prevista: trocar o reler por "const reler = () => void qc"
  // (nada invalida; o teste de ativar também fica vermelho: o "Ativada." vem do /me relido).
  it('abrir e fechar um diálogo do 2FA relê o /me', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    await screen.findByRole('dialog')
    await waitFor(() => expect(r.falsa.chamadas('GET /me')).toBe(2))
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(r.falsa.chamadas('GET /me')).toBe(3))
  })

  // Simétrico: com o 2FA ligado em outra aba, "Ativar" não abre (o enroll
  // responderia 403). Sabotagem prevista: trocar
  // "dialogo === 'ativar' && (totp === false || travado)" por
  // "dialogo === 'ativar' && (totp === false || travado || true)".
  it('com o 2FA ligado em outra aba, "Ativar" não abre o formulário', async () => {
    const r = await renderizarPainel('/settings')
    const ativar = await screen.findByRole('button', { name: 'Ativar' })
    r.falsa.estado.totpLigado = true
    await r.usuario.click(ativar)
    expect(await screen.findByRole('button', { name: 'Desativar' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/enroll')).toBe(0)
  })

  // PENDENCIAS do Plano 6: sem 2FA ligado, "desativar" não se oferece (o
  // POST /auth/totp/disable contaria tentativa falha, R34). Outra aba o
  // desligou: o /me relido ao abrir não deixa o diálogo aparecer.
  // Sabotagem prevista: trocar "dialogo === 'desativar' && totp === true"
  // por "dialogo === 'desativar' && (totp === true || true)".
  it('com o 2FA desligado em outra aba, "Desativar" não abre o formulário', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    const desativar = await screen.findByRole('button', { name: 'Desativar' })
    r.falsa.estado.totpLigado = false
    await r.usuario.click(desativar)
    expect(await screen.findByRole('button', { name: 'Ativar' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/disable')).toBe(0)
  })
})
