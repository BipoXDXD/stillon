import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { CODIGOS_NOVOS, RECUPERACAO_OK, rotacao, SEGREDO_NOVO, TOTP_NOVO, TOTP_OK } from '@/test/api-falsa'
import { sessao as familia, me } from '@/test/fixtures'
import { renderizarPainel, SENHA } from '@/test/renderizar'
import { ATRASO_DA_REVOGACAO } from './PaginaConfiguracoes'
import { servidor } from '@/test/setup'

// Uma resposta segurada até o teste soltar.
function portao() {
  let soltar!: () => void
  const aberto = new Promise<void>((r) => (soltar = r))
  return { aberto, soltar }
}

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

  // pendência do Plano 6: o código de recuperação é gasto antes da
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

// A3 do Plano 10, depois da integração com o auth: a 5ª prova errada na
// família derruba a sessão (401 unauthorized). Na tela é o mesmo caminho da
// família morta: "O acesso desta tela venceu.", e renovar leva ao login.
describe('provas erradas na família', () => {
  it('a 5ª senha errada no diálogo do 2FA derruba a sessão e volta ao login', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    const senha = within(dialogo).getByLabelText('Sua senha')
    for (let i = 1; i < 5; i++) {
      await r.usuario.clear(senha)
      await r.usuario.type(senha, 'errada')
      await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
      await waitFor(() => expect(r.falsa.chamadas('POST /auth/totp/enroll')).toBe(i))
      expect(await within(dialogo).findByRole('alert')).not.toHaveTextContent('O acesso desta tela venceu.')
    }
    expect(r.falsa.estado.familiaRevogada).toBe(false)
    await r.usuario.clear(senha)
    await r.usuario.type(senha, 'errada')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await waitFor(() => expect(within(dialogo).getByRole('alert')).toHaveTextContent('O acesso desta tela venceu.'))
    expect(r.falsa.estado.familiaRevogada).toBe(true)
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

  // Revisão final: o Confirmar das sessões não tinha "ocupado"; um clique
  // duplo na sessão atual mandava dois DELETE, e o segundo (401, a família
  // já morta) trocava o motivo para "expirou". Sabotagens: tirar o
  // ocupado={ocupado} do Confirmar das sessões (Cancelar não desabilita);
  // somado a isso, a guarda "|| ocupado" de encerrar() → 2 DELETE.
  it('clique duplo em Encerrar a sessão atual manda um DELETE só, e o motivo fica', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Encerrar' }))
    const dialogo = await screen.findByRole('alertdialog')
    const acao = within(dialogo).getByRole('button', { name: 'Encerrar' })
    // O DELETE demora: o diálogo fica ocupado (Cancelar desabilitado) enquanto isso.
    const p = portao()
    servidor.use(
      http.delete('*/api/v1/sessions/:id', async () => {
        await p.aberto
      }),
    )
    fireEvent.click(acao)
    fireEvent.click(acao)
    await waitFor(() => expect(within(dialogo).getByRole('button', { name: 'Cancelar' })).toBeDisabled())
    fireEvent.click(acao)
    p.soltar()
    expect(await screen.findByText('Você encerrou esta sessão.')).toBeInTheDocument()
    expect(r.falsa.chamadas('DELETE /sessions/:id')).toBe(1)
  })

  // I1: o "Encerrar" que abriu o diálogo some com a linha; o foco vai ao
  // título da tela. Antes, o diálogo fechava ANTES de a lista mudar: o foco
  // voltava ao botão, que sumia em seguida. Sabotagem: em encerrar(), pôr o
  // setAlvo(null) antes do await da lista → o foco cai no body.
  it('encerrar outra sessão não deixa o foco no <body>', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.sessoes = [familia(), familia({ id: 'fam-2', current: false })]
    await r.cliente.refetchQueries()
    const botoes = await screen.findAllByRole('button', { name: 'Encerrar' })
    await r.usuario.click(botoes[1]!)
    // A lista relida depois do DELETE demora (a rede de verdade): é o que
    // separa "fechar e depois a linha sumir" de "a linha sumir e depois fechar".
    const p = portao()
    servidor.use(
      http.get('*/api/v1/sessions', async () => {
        await p.aberto
        return HttpResponse.json({ items: [familia()] })
      }),
    )
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Encerrar' }))
    await waitFor(() => expect(r.falsa.chamadas('DELETE /sessions/:id')).toBe(1))
    await new Promise((res) => setTimeout(res, 50))
    p.soltar()
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Encerrar' })).toHaveLength(1))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(document.activeElement).not.toBe(document.body))
    expect(screen.getByRole('heading', { name: 'Configurações', level: 1 })).toHaveFocus()
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

  // M2 da revisão final: um envio vazio nos diálogos do 2FA ia ao servidor
  // e contava como tentativa falha no bloqueio progressivo (R34). Agora o
  // formulário o segura. Sabotagens: trocar o esquema de um dos formulários
  // (a senha do Ativar, o código do Ativar, o código do Desativar) por um
  // sem min(1) → o teste fica vermelho no passo correspondente.
  it('Enter vazio nos diálogos do 2FA não chama o servidor', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    let dialogo = await screen.findByRole('dialog')
    await r.usuario.click(within(dialogo).getByLabelText('Sua senha'))
    await r.usuario.keyboard('{Enter}')
    expect(await within(dialogo).findByText('Informe a senha.')).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/enroll')).toBe(0)
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.keyboard('{Enter}')
    await r.usuario.click(await within(dialogo).findByLabelText('Código de 6 dígitos'))
    await r.usuario.keyboard('{Enter}')
    expect(await within(dialogo).findByText('Informe o código.')).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/confirm')).toBe(0)
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Desativar' }))
    dialogo = await screen.findByRole('dialog')
    await r.usuario.click(within(dialogo).getByLabelText('Sua senha'))
    await r.usuario.keyboard('{Enter}')
    expect(await within(dialogo).findByText('Informe a senha.')).toBeInTheDocument()
    expect(within(dialogo).getByText('Informe o código.')).toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.keyboard('{Enter}')
    await waitFor(() => expect(within(dialogo).queryByText('Informe a senha.')).not.toBeInTheDocument())
    expect(within(dialogo).getByText('Informe o código.')).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/disable')).toBe(0)
  })

  // M4 da revisão final: copiar os códigos de recuperação tinha zero
  // feedback (nem no sucesso, nem na falha). Agora é o useCopiar, igual à
  // status page. Sabotagem: voltar ao onClick antigo
  // ("void navigator.clipboard?.writeText(...)") → nenhum aviso.
  it('copiar os códigos de recuperação avisa o sucesso e a falha', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    await r.usuario.click(await within(dialogo).findByRole('button', { name: 'Copiar' }))
    expect((await screen.findAllByText('Códigos copiados.'))[0]).toBeInTheDocument()
    expect(await navigator.clipboard.readText()).toBe('AAAABBBBCCCCDDDD\nEEEEFFFFGGGGHHHH')
    const falha = vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValueOnce(new Error('negado'))
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Copiado' }))
    expect((await screen.findAllByText(/Não foi possível copiar/))[0]).toBeInTheDocument()
    expect(within(dialogo).getByRole('button', { name: 'Copiar' })).toBeInTheDocument()
    falha.mockRestore()
  })

  // Revisão final: o 2FA ligado em outra aba com o diálogo de ativar já
  // aberto. O enroll (e o confirm) respondem 403, que aparecia como
  // "Operação não permitida.". Agora: o texto certo e o /me relido (que
  // fecha o diálogo). Sabotagem: em aoJaAtivada, tirar o reler() → o
  // diálogo fica aberto com o botão "Ativar" na tela.
  for (const passo of ['enroll', 'confirm'] as const) {
    it(`403 no ${passo}: "já está ativada", e o /me relido`, async () => {
      const r = await renderizarPainel('/settings')
      await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
      const dialogo = await screen.findByRole('dialog')
      if (passo === 'confirm') {
        await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
        await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
        await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_OK)
        r.falsa.estado.totpLigado = true
        await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
      } else {
        await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
        r.falsa.estado.totpLigado = true
        await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
      }
      expect((await screen.findAllByText('A verificação em duas etapas já está ativada.'))[0]).toBeInTheDocument()
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
      expect(await screen.findByRole('button', { name: 'Desativar' })).toBeInTheDocument()
      expect(screen.queryByText('Operação não permitida.')).not.toBeInTheDocument()
    })
  }

  // Revisão final: o Firefox cancela o download se a URL do blob é revogada
  // logo depois do click(), e o único arquivo dos códigos não chegava. A
  // revogação vai para um setTimeout longo. Sabotagem: revogar logo depois
  // do a.click() → o revoke já foi chamado na volta do clique.
  it('Baixar .txt revoga a URL do arquivo só depois, e não logo após o clique', async () => {
    const criar = vi.fn<(o: Blob | MediaSource) => string>(() => 'blob:codigos')
    const revogar = vi.fn<(url: string) => void>()
    const antes = { criar: URL.createObjectURL, revogar: URL.revokeObjectURL }
    URL.createObjectURL = criar
    URL.revokeObjectURL = revogar
    const clique = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    try {
      const r = await renderizarPainel('/settings')
      await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
      const dialogo = await screen.findByRole('dialog')
      await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
      await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
      await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_OK)
      await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
      const baixar = await within(dialogo).findByRole('button', { name: 'Baixar .txt' })
      const agendar = vi.spyOn(window, 'setTimeout')
      await r.usuario.click(baixar)
      expect(clique).toHaveBeenCalledTimes(1)
      expect(revogar).not.toHaveBeenCalled()
      const agendado = agendar.mock.calls.find(([, ms]) => ms === ATRASO_DA_REVOGACAO)
      agendar.mockRestore()
      expect(agendado).toBeDefined()
      ;(agendado![0] as () => void)()
      expect(revogar).toHaveBeenCalledWith('blob:codigos')
    } finally {
      clique.mockRestore()
      URL.createObjectURL = antes.criar
      URL.revokeObjectURL = antes.revogar
    }
  })

  // pendência do Plano 6: trocar a senha apaga o cadastro pendente, e o
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
  // ativar, trocar "travado={preso}" por "travado={preso && Math.random() > 2}".
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
    // continua, pelo travado. Sabotagem prevista: no portão, trocar
    // "totp === false || preso" por "totp === false && (preso || !preso)".
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    await waitFor(() => expect(r.falsa.chamadas('GET /me')).toBeGreaterThanOrEqual(3))
    expect(screen.getByRole('dialog')).toBe(dialogo)
    expect(within(dialogo).getByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
  })

  // I2/M11: ['me'] não vence sozinho; abrir e fechar os diálogos do 2FA o
  // releem. Sabotagens previstas: em abrir(), "staleTime: 0" por
  // "staleTime: Infinity" (não relê ao abrir); e o reler por
  // "const reler = () => void qc" (não relê ao fechar; o teste de ativar
  // também fica vermelho: o "Ativada." vem do /me relido).
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
  // responderia 403). Sabotagem prevista: em abrir(), trocar
  // "if (d === 'ativar' && ligado)" por "if (d === 'ativar' && ligado && Math.random() > 2)"
  // (sem o aviso; o portão ainda fecha o diálogo, e é a outra camada).
  it('com o 2FA ligado em outra aba, "Ativar" não abre o formulário', async () => {
    const r = await renderizarPainel('/settings')
    const ativar = await screen.findByRole('button', { name: 'Ativar' })
    r.falsa.estado.totpLigado = true
    await r.usuario.click(ativar)
    expect(await screen.findByRole('button', { name: 'Desativar' })).toBeInTheDocument()
    expect(await screen.findByText('A verificação em duas etapas já está ativada.')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/enroll')).toBe(0)
  })

  // pendência do Plano 6: sem 2FA ligado, "desativar" não se oferece (o
  // POST /auth/totp/disable contaria tentativa falha, R34). Outra aba o
  // desligou: o /me relido ao abrir não deixa o diálogo aparecer.
  // Sabotagem prevista: em abrir(), trocar "d === 'desativar' && !ligado"
  // por "d === 'desativar' && !ligado && Math.random() > 2".
  it('com o 2FA desligado em outra aba, "Desativar" não abre o formulário', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    const desativar = await screen.findByRole('button', { name: 'Desativar' })
    r.falsa.estado.totpLigado = false
    await r.usuario.click(desativar)
    expect(await screen.findByRole('button', { name: 'Ativar' })).toBeInTheDocument()
    expect(await screen.findByText('A verificação em duas etapas já está desativada.')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas('POST /auth/totp/disable')).toBe(0)
  })

  // Revisão final: o portão do 2FA decidia pelo /me do cache, antes de o
  // /me relido chegar; com o /me lento, "Desativar" abria com o dado velho.
  // Sabotagem: em abrir(), não esperar o refetch ("void qc.refetchQueries")
  // → o diálogo aparece enquanto o /me está segurado.
  it('"Desativar" espera o /me relido: com ele lento, não abre com o cache velho', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    const desativar = await screen.findByRole('button', { name: 'Desativar' })
    const p = portao()
    servidor.use(
      http.get('*/api/v1/me', async () => {
        await p.aberto
        return HttpResponse.json(me({ totp_enabled: false }))
      }),
    )
    await r.usuario.click(desativar)
    // Um tique do Radix e do React: um diálogo que fosse abrir já estaria lá.
    await new Promise((res) => setTimeout(res, 50))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    p.soltar()
    expect(await screen.findByRole('button', { name: 'Ativar' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  // Sem o /me relido não há como decidir: nada abre, e o erro aparece.
  // Sabotagem: no catch de abrir(), trocar o "return" por "ligado = false".
  it('se o /me relido falha, o diálogo não abre e o erro é avisado', async () => {
    const r = await renderizarPainel('/settings')
    const ativar = await screen.findByRole('button', { name: 'Ativar' })
    r.falsa.estado.forcar.set('GET /me', { status: 429, code: 'rate_limited' })
    await r.usuario.click(ativar)
    expect(await screen.findByText('Muitas requisições. Aguarde um minuto e tente de novo.')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  // O portão cai com o diálogo aberto (o /me relido diz que outra aba
  // desligou o 2FA): o diálogo fecha de vez, e não reaparece sozinho se o
  // /me voltar a dizer "ligado". Sabotagem: tirar o setDialogo(null) do
  // portão caído → o diálogo reabre no último refetch.
  it('se o 2FA muda com o diálogo aberto, ele fecha e não reabre sozinho', async () => {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Desativar' }))
    await screen.findByRole('dialog')
    r.falsa.estado.totpLigado = false
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    expect(await screen.findByRole('button', { name: 'Desativar' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})

// Spec do Plano 9 §4.2: trocar de autenticador sem desligar o 2FA.
describe('troca de autenticador', () => {
  const ROTATE = 'POST /auth/totp/rotate'
  const CONFIRM = 'POST /auth/totp/rotate/confirm'

  async function comTotp() {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    return r
  }

  // Abre o diálogo e passa pelo passo 1 com as provas dadas.
  async function iniciar(r: Awaited<ReturnType<typeof renderizarPainel>>, codigo = TOTP_OK) {
    await r.usuario.click(await screen.findByRole('button', { name: 'Trocar autenticador' }))
    const dialogo = await screen.findByRole('dialog', { name: 'Trocar o autenticador' })
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.type(within(dialogo).getByLabelText('Código do autenticador'), codigo)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    return dialogo
  }

  it('só aparece com o 2FA ligado, ao lado de Desativar', async () => {
    const r = await renderizarPainel('/settings')
    expect(await screen.findByRole('button', { name: 'Ativar' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Trocar autenticador' })).not.toBeInTheDocument()
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('button', { name: 'Trocar autenticador' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Desativar' })).toBeInTheDocument()
  })

  // Os três passos. Sabotagem (passo 3): no Dialogo de trocar, trocar
  // "travado={preso}" por "travado={preso && Math.random() > 2}" → o Esc
  // fecha e os códigos somem.
  it('três passos: provas, QR do app novo e os 10 códigos, que só fecham depois de guardados', async () => {
    const r = await comTotp()
    r.falsa.estado.sessoes = [familia(), familia({ id: 'fam-2', current: false })]
    await r.cliente.refetchQueries()
    expect(await screen.findAllByRole('button', { name: 'Encerrar' })).toHaveLength(2)
    const dialogo = await iniciar(r)
    // Passo 1: as mesmas provas do Desativar.
    expect(r.falsa.corpos(ROTATE)).toEqual([{ password: SENHA, code: TOTP_OK }])
    // Passo 2: o QR e a chave do app NOVO.
    expect(await within(dialogo).findByText(SEGREDO_NOVO)).toBeInTheDocument()
    expect(within(dialogo).getByTitle('QR code do autenticador')).toBeInTheDocument()
    expect(within(dialogo).getByText(/O app atual continua valendo até você confirmar/)).toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('Código de 6 dígitos'), TOTP_NOVO)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    // Passo 3: os 10 códigos novos.
    expect(await within(dialogo).findByText(CODIGOS_NOVOS[0]!)).toBeInTheDocument()
    expect(within(dialogo).getAllByRole('listitem')).toHaveLength(10)
    // O confirm leva o rotation que o rotate devolveu (A7 do Plano 10).
    expect(r.falsa.corpos(CONFIRM)).toEqual([{ code: TOTP_NOVO, rotation: rotacao(1) }])
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
    const concluir = within(dialogo).getByRole('button', { name: 'Concluir' })
    expect(concluir).toBeDisabled()
    await r.usuario.click(within(dialogo).getByLabelText('Guardei os códigos'))
    await r.usuario.click(concluir)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(await screen.findByText('Autenticador trocado. As outras sessões foram encerradas.')).toBeInTheDocument()
    // As outras sessões caíram: a lista relida mostra só esta.
    await waitFor(() => expect(screen.getAllByRole('button', { name: 'Encerrar' })).toHaveLength(1))
    expect(screen.queryByText('Nada mudou: o app atual continua valendo.')).not.toBeInTheDocument()
    expect(screen.getByText('Ativada.')).toBeInTheDocument()
  })

  it('código de recuperação serve de prova no passo 1', async () => {
    const r = await comTotp()
    const dialogo = await iniciar(r, RECUPERACAO_OK)
    expect(await within(dialogo).findByText(SEGREDO_NOVO)).toBeInTheDocument()
    expect(r.falsa.corpos(ROTATE)).toEqual([{ password: SENHA, code: RECUPERACAO_OK }])
  })

  // D24 do Plano 5: prova errada é "Senha ou código incorretos.", sem
  // refresh; o diálogo fica no passo 1, com o formulário.
  it('prova errada no passo 1: o texto de credencial, sem renovar, e o formulário fica', async () => {
    const r = await comTotp()
    const refreshes = r.falsa.chamadas('POST /auth/refresh')
    const dialogo = await iniciar(r, '000000')
    const alerta = await within(dialogo).findByRole('alert')
    expect(alerta).toHaveTextContent('Senha ou código incorretos.')
    // Um código de 6 dígitos não é de recuperação: nada de aviso de código gasto.
    expect(alerta).not.toHaveTextContent('código de recuperação')
    expect(within(dialogo).getByLabelText('Sua senha')).toHaveValue(SENHA)
    expect(r.falsa.chamadas(ROTATE)).toBe(1)
    expect(r.falsa.chamadas('POST /auth/refresh')).toBe(refreshes)
  })

  // E5 do Plano 10: como na troca de senha, um 401 depois de um código de
  // recuperação no passo 1 avisa que ele pode ter sido gasto. Sabotagem:
  // tirar o código do erroDeCredencial do passo 1 → o aviso some.
  it('401 no passo 1 com um código de recuperação avisa que ele pode ter sido consumido', async () => {
    const r = await comTotp()
    r.falsa.estado.forcar.set(ROTATE, { status: 401, code: 'unauthorized' })
    const dialogo = await iniciar(r, RECUPERACAO_OK)
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent(
      'O acesso desta tela venceu. Se você usou um código de recuperação, ele pode ter sido consumido; use outro.',
    )
    expect(within(dialogo).getByRole('button', { name: 'Renovar acesso e tentar de novo' })).toBeInTheDocument()
  })

  // Código errado no passo 2: o pendente fica, e o código certo depois
  // funciona (a perna positiva no mesmo diálogo).
  it('código errado no passo 2: "Código incorreto.", e o certo depois conclui', async () => {
    const r = await comTotp()
    const dialogo = await iniciar(r)
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), '000000')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('Código incorreto.')
    expect(within(dialogo).getByText(SEGREDO_NOVO)).toBeInTheDocument()
    await r.usuario.clear(within(dialogo).getByLabelText('Código de 6 dígitos'))
    await r.usuario.type(within(dialogo).getByLabelText('Código de 6 dígitos'), TOTP_NOVO)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    expect(await within(dialogo).findByText(CODIGOS_NOVOS[9]!)).toBeInTheDocument()
    expect(r.falsa.chamadas(CONFIRM)).toBe(2)
  })

  // 403 no rotate: outra aba desligou o 2FA. O diálogo fecha e avisa.
  // Sabotagem: em aoDesativada, não chamar fechar() (só avisar) → o
  // diálogo fica aberto.
  it('403 ao iniciar: o 2FA foi desligado em outra aba; o diálogo fecha e avisa', async () => {
    const r = await comTotp()
    await r.usuario.click(await screen.findByRole('button', { name: 'Trocar autenticador' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.type(within(dialogo).getByLabelText('Código do autenticador'), TOTP_OK)
    // Desligado em outra aba, mas o /me desta ainda diz "ligado" até ser relido.
    r.falsa.estado.forcar.set(ROTATE, { status: 403, code: 'forbidden' })
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    expect((await screen.findAllByText('A verificação em duas etapas já está desativada.'))[0]).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.queryByText('Operação não permitida.')).not.toBeInTheDocument()
    expect(r.falsa.chamadas(ROTATE)).toBe(1)
  })

  // 403 no confirm: a troca foi concluída ou cancelada em outra aba. O
  // diálogo volta ao passo 1, com o texto da spec, e recomeçar funciona.
  // Sabotagem: tratar o 403 do confirm como credencial (tirar o ramo do
  // 403) → aparece "Operação não permitida." no passo 2.
  // Sabotagem: omitir a recuperação gasta do erro de 403 mantendo a volta
  // ao passo 1. Só a perna de recuperação deve ficar vermelha.
  it.each([
    ['do app', TOTP_OK, false],
    ['de recuperação', RECUPERACAO_OK, true],
  ] as const)('403 ao confirmar preserva o aviso e recomeçar funciona (código %s)', async (_, codigo, gasto) => {
    const r = await comTotp()
    const dialogo = await iniciar(r, codigo)
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_NOVO)
    // Outra aba concluiu a troca: não há mais pendente.
    r.falsa.estado.troca = null
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    const alerta = await within(dialogo).findByRole('alert')
    expect(alerta).toHaveTextContent(
      'A troca foi concluída ou cancelada em outra aba. Comece de novo.',
    )
    expect(alerta.textContent?.includes('O código de recuperação usado foi gasto')).toBe(gasto)
    expect(screen.queryByText(/Nada mudou/)).not.toBeInTheDocument()
    expect(within(dialogo).queryByText(SEGREDO_NOVO)).not.toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.type(within(dialogo).getByLabelText('Código do autenticador'), TOTP_OK)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    expect(await within(dialogo).findByText(SEGREDO_NOVO)).toBeInTheDocument()
    expect(r.falsa.chamadas(ROTATE)).toBe(2)
  })

  // A7 do Plano 10: outra aba da MESMA sessão iniciou outra troca, e o
  // pendente deste QR foi substituído. O confirm leva o rotation deste QR e
  // recebe 403 (não 401 invalid_credentials, que contaria falha), e o
  // diálogo volta ao passo 1. Sabotagem: mandar no confirm o rotation mais
  // novo da API falsa, e não o do QR da tela → os códigos aparecem.
  it('pendente substituído por outra aba: o confirm com o rotation deste QR recomeça a troca', async () => {
    const r = await comTotp()
    const dialogo = await iniciar(r)
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_NOVO)
    // A outra aba fez o próprio rotate: o rotation mudou.
    r.falsa.estado.troca = { rotation: rotacao(2) }
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent(
      'A troca foi concluída ou cancelada em outra aba. Comece de novo.',
    )
    expect(r.falsa.corpos(CONFIRM)).toEqual([{ code: TOTP_NOVO, rotation: rotacao(1) }])
    expect(r.falsa.estado.troca).toEqual({ rotation: rotacao(2) })
  })

  // Fechar no passo 2 não muda nada: o aviso diz isso. Fechar no passo 1
  // não avisa (nada começou). Sabotagem: em fechar(), não olhar o passo
  // (tirar o aviso) → o texto não aparece.
  it('fechar no passo 2 avisa que nada mudou; no passo 1, não', async () => {
    const r = await comTotp()
    await r.usuario.click(await screen.findByRole('button', { name: 'Trocar autenticador' }))
    await screen.findByRole('dialog')
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const dialogo = await iniciar(r)
    await within(dialogo).findByText(SEGREDO_NOVO)
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(await screen.findAllByText('Nada mudou: o app atual continua valendo.')).toHaveLength(1)
    expect(screen.queryByText(/foi gasto/)).not.toBeInTheDocument()
    expect(r.falsa.chamadas(CONFIRM)).toBe(0)
  })

  // R1 da spec: o código de recuperação gasto no passo 1 fica gasto. O
  // "nada mudou" diz isso quando foi um código de recuperação.
  it('fechar no passo 2 depois de usar um código de recuperação avisa que ele foi gasto', async () => {
    const r = await comTotp()
    const dialogo = await iniciar(r, RECUPERACAO_OK)
    await within(dialogo).findByText(SEGREDO_NOVO)
    await r.usuario.keyboard('{Escape}')
    expect(
      await screen.findByText(
        'Nada mudou: o app atual continua valendo. O código de recuperação usado foi gasto; use outro da próxima vez.',
      ),
    ).toBeInTheDocument()
  })

  // Sabotagem: fechar pelo portão sem emitir aviso, ou apagar o contexto
  // da recuperação antes de compor o texto. O diálogo ainda fecha, mas a
  // orientação observável desaparece. O refetch seguinte não duplica toast.
  it.each([
    ['do app', TOTP_OK, false],
    ['de recuperação', RECUPERACAO_OK, true],
  ] as const)('/me desativado no QR fecha com aviso fiel ao estado (código %s)', async (_, codigo, gasto) => {
    const r = await comTotp()
    const dialogo = await iniciar(r, codigo)
    await within(dialogo).findByText(SEGREDO_NOVO)
    r.falsa.estado.totpLigado = false
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const avisos = await screen.findAllByText(/A verificação em duas etapas já está desativada\./)
    expect(avisos).toHaveLength(1)
    expect(avisos[0]?.textContent?.includes('O código de recuperação usado foi gasto')).toBe(gasto)
    expect(screen.queryByText(/Nada mudou/)).not.toBeInTheDocument()
    expect(r.falsa.chamadas(CONFIRM)).toBe(0)
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    expect(await screen.findByRole('button', { name: 'Trocar autenticador' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getAllByText(/A verificação em duas etapas já está desativada\./)).toHaveLength(1)
  })

  // Sabotagem: escolher o aviso genérico de desativação antes do confirm
  // incerto. Recuperação gasta é conhecida, mas o resultado da mutação não.
  it('/me desativado preserva confirmação incerta e recuperação gasta', async () => {
    const r = await comTotp()
    const dialogo = await iniciar(r, RECUPERACAO_OK)
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_NOVO)
    servidor.use(http.post('*/api/v1/auth/totp/rotate/confirm', () => HttpResponse.error(), { once: true }))
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    await within(dialogo).findByRole('alert')
    r.falsa.estado.totpLigado = false
    await r.cliente.refetchQueries({ queryKey: ['me'] })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const avisos = await screen.findAllByText(/A resposta do servidor não chegou, e não dá para saber se a troca foi concluída\./)
    expect(avisos).toHaveLength(1)
    expect(avisos[0]).toHaveTextContent('O código de recuperação usado foi gasto')
    expect(screen.queryByText(/Nada mudou/)).not.toBeInTheDocument()
    expect(screen.queryByText(/^A verificação em duas etapas já está desativada\./)).not.toBeInTheDocument()
  })

  // O reset perde o dado antigo enquanto relê: ausência não prova que o
  // 2FA foi desligado. Sabotagem: trocar o fallback null do aviso do portão
  // por JA_DESATIVADA; o diálogo fecha, mas surge uma afirmação sem /me.
  it('/me sem dado durante releitura fecha sem afirmar desativação ou resultado da troca', async () => {
    const r = await comTotp()
    const dialogo = await iniciar(r, RECUPERACAO_OK)
    await within(dialogo).findByText(SEGREDO_NOVO)
    const p = portao()
    servidor.use(http.get('*/api/v1/me', async () => {
      await p.aberto
      return HttpResponse.json(me({ totp_enabled: true }))
    }))
    const leitura = r.cliente.resetQueries({ queryKey: ['me'] })
    try {
      await screen.findByText('Carregando…')
      expect(r.cliente.getQueryData(['me'])).toBeUndefined()
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
      expect(screen.queryByText(/A verificação em duas etapas já está desativada\./)).not.toBeInTheDocument()
      expect(screen.queryByText(/Nada mudou/)).not.toBeInTheDocument()
    } finally {
      p.soltar()
      await leitura
    }
    expect(await screen.findByRole('button', { name: 'Trocar autenticador' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas(CONFIRM)).toBe(0)
  })

  // Enter vazio não chega ao servidor (R34), nos dois passos com envio.
  // Sabotagem: no passo 1, trocar o esquema por um sem min(1) → o rotate é
  // chamado.
  it('Enter vazio na troca não chama o servidor', async () => {
    const r = await comTotp()
    await r.usuario.click(await screen.findByRole('button', { name: 'Trocar autenticador' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.click(within(dialogo).getByLabelText('Sua senha'))
    await r.usuario.keyboard('{Enter}')
    expect(await within(dialogo).findByText('Informe a senha.')).toBeInTheDocument()
    expect(within(dialogo).getByText('Informe o código.')).toBeInTheDocument()
    expect(r.falsa.chamadas(ROTATE)).toBe(0)
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.type(within(dialogo).getByLabelText('Código do autenticador'), TOTP_OK)
    await r.usuario.keyboard('{Enter}')
    await r.usuario.click(await within(dialogo).findByLabelText('Código de 6 dígitos'))
    await r.usuario.keyboard('{Enter}')
    expect(await within(dialogo).findByText('Informe o código.')).toBeInTheDocument()
    expect(r.falsa.chamadas(CONFIRM)).toBe(0)
    expect(r.falsa.chamadas(ROTATE)).toBe(1)
  })

  // A trava do /me: com o 2FA desligado em outra aba, "Trocar autenticador"
  // não abre (o rotate responderia 403 e gastaria uma tentativa). Sabotagem:
  // em abrir(), trocar "d !== 'ativar' && !ligado" por
  // "d === 'desativar' && !ligado" → o diálogo de trocar abre.
  it('com o 2FA desligado em outra aba, "Trocar autenticador" não abre', async () => {
    const r = await comTotp()
    const trocar = await screen.findByRole('button', { name: 'Trocar autenticador' })
    r.falsa.estado.totpLigado = false
    await r.usuario.click(trocar)
    expect(await screen.findByRole('button', { name: 'Ativar' })).toBeInTheDocument()
    expect(await screen.findByText('A verificação em duas etapas já está desativada.')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(r.falsa.chamadas(ROTATE)).toBe(0)
  })

  // E6 do Plano 10: o "Trocar autenticador" clicado some quando o /me
  // relido diz "desligado", e o foco ia junto para o <body>. Ele vai ao
  // botão que fica (agora "Ativar"). Sabotagem: tirar o efeito do
  // BotaoQueSome (usar o Botao direto) → o foco fica no <body>.
  it('"Trocar autenticador" que some com o 2FA desligado em outra aba leva o foco ao "Ativar"', async () => {
    const r = await comTotp()
    const trocar = await screen.findByRole('button', { name: 'Trocar autenticador' })
    r.falsa.estado.totpLigado = false
    await r.usuario.click(trocar)
    const ativar = await screen.findByRole('button', { name: 'Ativar' })
    expect(trocar).not.toBeInTheDocument()
    expect(ativar).toHaveFocus()
  })

  // A trava espera o /me relido: com ele lento, nada abre pelo cache.
  // Sabotagem: no onClick do botão, "void abrir('trocar')" por
  // "setDialogo('trocar')" (abrir sem reler o /me) → o diálogo aparece
  // enquanto o /me está segurado.
  it('"Trocar autenticador" espera o /me relido antes de abrir', async () => {
    const r = await comTotp()
    const trocar = await screen.findByRole('button', { name: 'Trocar autenticador' })
    const antes = r.falsa.chamadas('GET /me')
    const p = portao()
    servidor.use(
      http.get('*/api/v1/me', async () => {
        await p.aberto
        return HttpResponse.json(me({ totp_enabled: true }))
      }),
    )
    await r.usuario.click(trocar)
    await new Promise((res) => setTimeout(res, 50))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    p.soltar()
    expect(await screen.findByRole('dialog', { name: 'Trocar o autenticador' })).toBeInTheDocument()
    // O /me relido foi o do handler segurado, não o da API falsa.
    expect(r.falsa.chamadas('GET /me')).toBe(antes)
  })
})

// Revisão final do Plano 9: os envios dos diálogos do 2FA com a resposta
// ainda em voo — clique duplo (F1) e fechar no meio (F2).
describe('envio em voo nos diálogos do 2FA', () => {
  const ROTATE = 'POST /auth/totp/rotate'
  const CONFIRM = 'POST /auth/totp/rotate/confirm'
  const TROCA_SUMIU = 'A troca foi concluída ou cancelada em outra aba. Comece de novo.'
  const TROCA_INCERTA =
    'A resposta do servidor não chegou, e não dá para saber se a troca foi concluída. Saia e entre com um código do app novo: se ele valer, a troca foi feita e os códigos de recuperação antigos não valem mais (troque o autenticador de novo para gerar outros); se não valer, o app atual continua valendo.'
  const ATIVACAO_INCERTA =
    'A resposta do servidor não chegou, e não dá para saber se a verificação em duas etapas foi ativada. Feche e confira nesta tela: se ela estiver ativada, os códigos de recuperação desta ativação se perderam; use "Trocar autenticador" para gerar outros.'
  const tique = () => new Promise((res) => setTimeout(res, 50))

  async function comTotp() {
    const r = await renderizarPainel('/settings')
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    return r
  }

  // Segura a rota até o teste soltar; depois, a API falsa responde (e conta).
  function segurar(metodoECaminho: string) {
    const p = portao()
    const estado = { chegou: 0 }
    servidor.use(
      http.post(`*/api/v1${metodoECaminho}`, async () => {
        estado.chegou++
        await p.aberto
      }),
    )
    return { ...p, estado }
  }

  async function abrirTroca(r: Awaited<ReturnType<typeof renderizarPainel>>) {
    await r.usuario.click(await screen.findByRole('button', { name: 'Trocar autenticador' }))
    const dialogo = await screen.findByRole('dialog', { name: 'Trocar o autenticador' })
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.type(within(dialogo).getByLabelText('Código do autenticador'), TOTP_OK)
    return dialogo
  }

  async function noPassoDoQr(r: Awaited<ReturnType<typeof renderizarPainel>>) {
    const dialogo = await abrirTroca(r)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_NOVO)
    return dialogo
  }

  // F1: o 2º confirm de um clique duplo voltava 403 (o pendente já tinha
  // sido consumido pelo 1º) e levava o diálogo de volta ao passo 1, com
  // "concluída ou cancelada em outra aba": os 10 códigos novos nunca
  // apareciam. Sabotagem: tirar a guarda de envio único do PassoDoQr → 2
  // confirms (e, tirando também o "ignorar se já está nos códigos" do catch,
  // os códigos somem).
  it('clique duplo em "Confirmar a troca" manda um confirm só, e os 10 códigos aparecem', async () => {
    const r = await comTotp()
    const dialogo = await noPassoDoQr(r)
    const s = segurar('/auth/totp/rotate/confirm')
    const confirmar = within(dialogo).getByRole('button', { name: 'Confirmar a troca' })
    fireEvent.click(confirmar)
    fireEvent.click(confirmar)
    await waitFor(() => expect(s.estado.chegou).toBeGreaterThanOrEqual(1))
    await tique()
    s.soltar()
    expect(await within(dialogo).findByText(CODIGOS_NOVOS[9]!)).toBeInTheDocument()
    // Um tique para uma 2ª resposta, se houvesse, chegar e desfazer a tela.
    await tique()
    expect(within(dialogo).getAllByRole('listitem')).toHaveLength(10)
    expect(screen.queryByText(TROCA_SUMIU)).not.toBeInTheDocument()
    expect(r.falsa.chamadas(CONFIRM)).toBe(1)
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
  })

  // F1, no passo 1: o clique duplo em "Continuar" mandava dois rotates.
  // Sabotagem: tirar a guarda de envio único do FormularioDeProvas → 2.
  it('clique duplo em "Continuar" manda um rotate só', async () => {
    const r = await comTotp()
    const dialogo = await abrirTroca(r)
    const s = segurar('/auth/totp/rotate')
    const continuar = within(dialogo).getByRole('button', { name: 'Continuar' })
    fireEvent.click(continuar)
    fireEvent.click(continuar)
    await waitFor(() => expect(s.estado.chegou).toBeGreaterThanOrEqual(1))
    await tique()
    s.soltar()
    expect(await within(dialogo).findByText(SEGREDO_NOVO)).toBeInTheDocument()
    await tique()
    expect(r.falsa.chamadas(ROTATE)).toBe(1)
  })

  // E1 do Plano 10: o passo 1 também trava o diálogo com o rotate em voo.
  // Antes, o Esc fechava, e a resposta chegava a um diálogo fechado: o
  // aoIniciar marcava a troca "no meio" sem diálogo (e, com um código de
  // recuperação, ele era gasto sem aviso). Sabotagem: tirar o
  // aoOcupar(true) do passo 1 → o Esc fecha.
  it('troca: Esc com o rotate em voo não fecha; o QR aparece', async () => {
    const r = await comTotp()
    const dialogo = await abrirTroca(r)
    const s = segurar('/auth/totp/rotate')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await waitFor(() => expect(s.estado.chegou).toBe(1))
    await r.usuario.keyboard('{Escape}')
    await tique()
    expect(screen.getByRole('dialog')).toBe(dialogo)
    s.soltar()
    expect(await within(dialogo).findByText(SEGREDO_NOVO)).toBeInTheDocument()
    // Solto de novo: no passo 2, fechar volta a valer (com o "nada mudou").
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(await screen.findAllByText('Nada mudou: o app atual continua valendo.')).toHaveLength(1)
  })

  // F2: Esc com o confirm em voo fechava o diálogo com "Nada mudou" (falso
  // se o confirm desse certo), os códigos se perdiam e o aoTravar, rodando
  // depois, deixava o travado preso. Agora o diálogo trava antes de aguardar
  // o confirm. E, concluído, o diálogo seguinte fecha com Esc. Sabotagens:
  // não travar durante o envio (tirar o "ocupado" do travado do Dialogo) →
  // o Esc fecha; não zerar travado/ocupado ao fechar nem ao abrir → o
  // diálogo seguinte não fecha com Esc.
  it('troca: Esc com o confirm em voo não fecha; os códigos aparecem, e o diálogo seguinte fecha com Esc', async () => {
    const r = await comTotp()
    const dialogo = await noPassoDoQr(r)
    const s = segurar('/auth/totp/rotate/confirm')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    await waitFor(() => expect(s.estado.chegou).toBe(1))
    await r.usuario.keyboard('{Escape}')
    await tique()
    expect(screen.getByRole('dialog')).toBe(dialogo)
    expect(screen.queryByText('Nada mudou: o app atual continua valendo.')).not.toBeInTheDocument()
    s.soltar()
    expect(await within(dialogo).findByText(CODIGOS_NOVOS[0]!)).toBeInTheDocument()
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
    await r.usuario.click(within(dialogo).getByLabelText('Guardei os códigos'))
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Concluir' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    // O seguinte é outra troca: o Desativar não usa o travado, e com ele o
    // teste não veria um travado preso.
    await r.usuario.click(screen.getByRole('button', { name: 'Trocar autenticador' }))
    await screen.findByRole('dialog', { name: 'Trocar o autenticador' })
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  // E2 e E3 do Plano 10: a resposta do confirm se perdeu (um 502 do proxy
  // sem o envelope, a rede, o prazo). A troca pode ter sido gravada: o
  // diálogo diz que não sabe, e nunca "Nada mudou". O fechamento usa o
  // mesmo texto. Sabotagem: tirar o ramo da resposta perdida do confirm
  // da troca → "O servidor respondeu algo inesperado." e, ao fechar, "Nada
  // mudou".
  it('troca: confirm sem resposta legível diz que não se sabe se a troca foi concluída', async () => {
    const r = await comTotp()
    const dialogo = await noPassoDoQr(r)
    servidor.use(
      http.post('*/api/v1/auth/totp/rotate/confirm', () => new HttpResponse('<html>Bad Gateway</html>', { status: 502 }), {
        once: true,
      }),
    )
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent(TROCA_INCERTA)
    // O confirm tinha passado (o 502 veio do proxy, depois): de novo, 403.
    // Continua sem saber, e não volta ao passo 1 como "outra aba".
    r.falsa.estado.troca = null
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    await waitFor(() => expect(r.falsa.chamadas(CONFIRM)).toBe(1))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent(TROCA_INCERTA)
    expect(within(dialogo).queryByText(TROCA_SUMIU)).not.toBeInTheDocument()
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect((await screen.findAllByText(TROCA_INCERTA))[0]).toBeInTheDocument()
    expect(screen.queryByText(/Nada mudou/)).not.toBeInTheDocument()
  })

  // E3: o confirm tem prazo. Sem ele, o diálogo ficava travado para
  // sempre com a rede presa. Sabotagem: tirar o sinal do totpConfirmarTroca
  // → o diálogo continua travado, sem texto.
  it('troca: confirm que não responde no prazo destrava e diz que não se sabe', async () => {
    const r = await renderizarPainel('/settings', { prazoDoConfirmMs: 100 })
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    const dialogo = await noPassoDoQr(r)
    servidor.use(http.post('*/api/v1/auth/totp/rotate/confirm', () => new Promise<never>(() => {})))
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Confirmar a troca' }))
    expect(await within(dialogo).findByRole('alert', {}, { timeout: 3000 })).toHaveTextContent(TROCA_INCERTA)
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  // I1 da revisão do Plano 10: o passo 1 da troca trava o diálogo (E1), e o
  // rotate não tinha prazo. Com a rede presa, o diálogo ficava travado para
  // sempre, sem botão de fechar. Agora o rotate tem o prazo dos confirms: no
  // estouro o diálogo destrava com "Sem conexão…" e, se a prova era um código
  // de recuperação, o aviso de que ele pode ter sido gasto (o rotate pode ter
  // comitado). Sabotagens: tirar o sinal do totpTrocar → o diálogo continua
  // travado, sem texto; tirar a resposta perdida do aviso de código de
  // recuperação → o caso de recuperação fica sem o aviso.
  it.each([
    ['do app', TOTP_OK, false],
    ['de recuperação', RECUPERACAO_OK, true],
  ] as const)('troca: rotate que não responde no prazo destrava o diálogo (código %s)', async (_, codigo, gasto) => {
    const r = await renderizarPainel('/settings', { prazoDoConfirmMs: 100 })
    r.falsa.estado.totpLigado = true
    await r.cliente.refetchQueries()
    servidor.use(http.post('*/api/v1/auth/totp/rotate', () => new Promise<never>(() => {})))
    await r.usuario.click(await screen.findByRole('button', { name: 'Trocar autenticador' }))
    const dialogo = await screen.findByRole('dialog', { name: 'Trocar o autenticador' })
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.type(within(dialogo).getByLabelText('Código do autenticador'), codigo)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await tique()
    // Travado enquanto espera: o Esc não fecha.
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
    const alerta = await within(dialogo).findByRole('alert', {}, { timeout: 3000 })
    expect(alerta).toHaveTextContent('Sem conexão com o servidor')
    expect(alerta.textContent?.includes('pode ter sido consumido')).toBe(gasto)
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  // A mesma coisa na ativação (totpConfirmar): sem rede ou sem resposta no
  // prazo, o 2FA pode ter sido ligado. Sabotagens: tirar o ramo da resposta
  // perdida da ativação → "Sem conexão…"; tirar o sinal do totpConfirmar →
  // o diálogo continua travado.
  it('ativação: confirm sem rede ou sem resposta no prazo diz que não se sabe se ativou', async () => {
    const r = await renderizarPainel('/settings', { prazoDoConfirmMs: 100 })
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_OK)
    servidor.use(http.post('*/api/v1/auth/totp/confirm', () => HttpResponse.error(), { once: true }))
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent(ATIVACAO_INCERTA)
    servidor.use(http.post('*/api/v1/auth/totp/confirm', () => new Promise<never>(() => {})))
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    await tique()
    // Travado enquanto espera: o Esc não fecha.
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
    await waitFor(() => expect(within(dialogo).getByRole('button', { name: 'Ativar' })).not.toHaveAttribute('aria-disabled'), {
      timeout: 3000,
    })
    expect(within(dialogo).getByRole('alert')).toHaveTextContent(ATIVACAO_INCERTA)
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect((await screen.findAllByText(ATIVACAO_INCERTA))[0]).toBeInTheDocument()
  })

  // Rodada complementar (decisão do usuário): o "Continuar" do passo da senha
  // da ativação também mandava dois enroll num clique duplo, e o QR na tela era
  // o da última resposta a chegar, não necessariamente o do segredo que ficou
  // pendente no servidor. Cada enroll aqui devolve um segredo diferente.
  // Sabotagem: tirar a guarda de envio único do passo da senha → 2 enroll.
  it('clique duplo em "Continuar" da ativação manda um enroll só, e o QR é o dele', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    const p = portao()
    const segredos = ['PRIMEIROENROLLAA', 'SEGUNDOENROLLBBB']
    let enrolls = 0
    servidor.use(
      http.post('*/api/v1/auth/totp/enroll', async () => {
        const segredo = segredos[enrolls++]!
        await p.aberto
        return HttpResponse.json({
          otpauth_uri: `otpauth://totp/StillOn:dono%40exemplo.com?secret=${segredo}&issuer=StillOn`,
        })
      }),
    )
    const continuar = within(dialogo).getByRole('button', { name: 'Continuar' })
    fireEvent.click(continuar)
    fireEvent.click(continuar)
    await waitFor(() => expect(enrolls).toBeGreaterThanOrEqual(1))
    await tique()
    p.soltar()
    expect(await within(dialogo).findByText('PRIMEIROENROLLAA')).toBeInTheDocument()
    await tique()
    expect(enrolls).toBe(1)
    expect(within(dialogo).queryByText('SEGUNDOENROLLBBB')).not.toBeInTheDocument()
  })

  // F2, a mesma corrida na ativação (totpConfirmar).
  it('ativação: Esc com o confirm em voo não fecha; os códigos aparecem, e o diálogo seguinte fecha com Esc', async () => {
    const r = await renderizarPainel('/settings')
    await r.usuario.click(await screen.findByRole('button', { name: 'Ativar' }))
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('Sua senha'), SENHA)
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Continuar' }))
    await r.usuario.type(await within(dialogo).findByLabelText('Código de 6 dígitos'), TOTP_OK)
    const s = segurar('/auth/totp/confirm')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Ativar' }))
    await waitFor(() => expect(s.estado.chegou).toBe(1))
    await r.usuario.keyboard('{Escape}')
    await tique()
    expect(screen.getByRole('dialog')).toBe(dialogo)
    s.soltar()
    expect(await within(dialogo).findByText('AAAABBBBCCCCDDDD')).toBeInTheDocument()
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBe(dialogo)
    await r.usuario.click(within(dialogo).getByLabelText('Guardei os códigos'))
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Concluir' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    await r.usuario.click(await screen.findByRole('button', { name: 'Trocar autenticador' }))
    await screen.findByRole('dialog', { name: 'Trocar o autenticador' })
    await r.usuario.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})
