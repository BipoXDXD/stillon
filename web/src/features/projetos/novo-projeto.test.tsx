import { act, screen, waitFor, within } from '@testing-library/react'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
import { PROJETO_PADRAO, type ApiFalsa } from '@/test/api-falsa'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

// Os prefixos das rotas de recurso (todas moram no projeto).
const RECURSOS = /^[A-Z]+ \/projects\/:projectId\/(targets|heartbeats|incidents|channels|status-pages|summary)\b/

function pedidosDeRecurso(falsa: ApiFalsa): string[] {
  return [...falsa.estado.chamadas.keys()].filter((k) => RECURSOS.test(k))
}

type Painel = Awaited<ReturnType<typeof renderizarPainel>>

// Abre o diálogo pelo item "Novo projeto" do seletor.
async function abrirPeloSeletor(r: Painel) {
  await r.usuario.click(await screen.findByRole('button', { name: /^Projeto: / }))
  await r.usuario.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Novo projeto' }))
  return screen.findByRole('dialog', { name: 'Novo projeto' })
}

const campo = () => screen.getByRole('textbox', { name: 'Nome do projeto' })

describe('novo projeto', () => {
  // Sabotagem: não invalidar a lista de projetos → nenhum GET depois do
  // POST; não navegar → fica no padrão.
  it('cria e entra', async () => {
    const r = await renderizarPainel(`/projects/${PROJETO_PADRAO}/`)
    await screen.findByRole('heading', { level: 1, name: 'Painel' })
    await abrirPeloSeletor(r)
    const leiturasAntes = r.falsa.chamadas('GET /projects')
    await waitFor(() => expect(campo()).toHaveFocus())
    await r.usuario.type(campo(), 'Clientes')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar projeto' }))

    await waitFor(() => expect(r.router.state.location.pathname).toBe('/projects/proj-2/'))
    expect(r.falsa.corpos('POST /projects')).toEqual([{ name: 'Clientes' }])
    expect(r.falsa.chamadas('GET /projects')).toBeGreaterThan(leiturasAntes)
    expect(await screen.findByRole('button', { name: 'Projeto: Clientes' })).toBeInTheDocument()
    expect(await screen.findByRole('heading', { level: 2, name: 'Nenhum serviço vigiado ainda.' })).toBeInTheDocument()
    expect((await screen.findAllByText('Projeto criado.')).length).toBeGreaterThan(0)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  // Sabotagem: tirar o min(1) do esquema → sai um POST.
  it('nome vazio não envia e o erro fica no campo', async () => {
    const r = await renderizarPainel(`/projects/${PROJETO_PADRAO}/`)
    await abrirPeloSeletor(r)
    await r.usuario.type(campo(), '   ')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar projeto' }))
    expect(await screen.findByText('Dê um nome ao projeto.')).toBeInTheDocument()
    expect(campo()).toHaveAttribute('aria-invalid', 'true')
    expect(campo()).toHaveAccessibleDescription('Dê um nome ao projeto.')
    expect(campo()).toHaveFocus()
    expect(r.falsa.chamadas('POST /projects')).toBe(0)
  })

  // O caractere invisível só o servidor recusa. Sabotagem: mostrar o erro de
  // campo como erro do formulário → o campo não fica inválido.
  it('o erro de campo do servidor aparece no campo, com o valor mantido', async () => {
    const r = await renderizarPainel(`/projects/${PROJETO_PADRAO}/`)
    await abrirPeloSeletor(r)
    await r.usuario.type(campo(), 'a​b')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar projeto' }))
    await waitFor(() => expect(campo()).toHaveAccessibleDescription('Tem um caractere que não pode ser mostrado.'))
    expect(campo()).toHaveAttribute('aria-invalid', 'true')
    expect(campo()).toHaveValue('a​b')
    expect(campo()).toHaveFocus()
  })

  // Sabotagem: tirar project_limit_reached de mensagens.ts → o texto
  // genérico; fechar o diálogo no erro → o campo some.
  it('no limite de projetos, o aviso fica no diálogo, com o valor', async () => {
    const r = await renderizarPainel(`/projects/${PROJETO_PADRAO}/`, {
      antes: (falsa) => {
        falsa.estado.limiteDeProjetos = 1
      },
    })
    await abrirPeloSeletor(r)
    await r.usuario.type(campo(), 'Clientes')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar projeto' }))
    const dialogo = screen.getByRole('dialog', { name: 'Novo projeto' })
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('Limite de projetos atingido.')
    expect(campo()).toHaveValue('Clientes')
    expect(screen.getByRole('button', { name: 'Criar projeto' })).not.toHaveAttribute('aria-disabled')
    expect(r.router.state.location.pathname).toBe(`/projects/${PROJETO_PADRAO}/`)
  })

  // O binário anterior no release ainda não tem o POST: o 405 pede recarregar.
  it('405 mostra o aviso de mensagemDe sem perder o texto', async () => {
    const r = await renderizarPainel(`/projects/${PROJETO_PADRAO}/`, {
      antes: (falsa) => falsa.estado.forcar.set('POST /projects', { status: 405, code: 'method_not_allowed' }),
    })
    await abrirPeloSeletor(r)
    await r.usuario.type(campo(), 'Clientes')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar projeto' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'O servidor não aceita esta operação. O painel pode estar desatualizado: recarregue a página.',
    )
    expect(campo()).toHaveValue('Clientes')
  })

  // Sabotagem: tirar a trava do envio único → dois POSTs; tirar o travado do
  // Dialogo → o Esc fecha no meio do envio.
  it('enquanto envia: "Criando…", um POST só e o diálogo não fecha', async () => {
    let soltar = () => {}
    const preso = new Promise<void>((resolve) => {
      soltar = resolve
    })
    const r = await renderizarPainel(`/projects/${PROJETO_PADRAO}/`)
    await abrirPeloSeletor(r)
    servidor.use(http.post('*/api/v1/projects', async () => { await preso }))
    await r.usuario.type(campo(), 'Clientes{Enter}')
    const botao = await screen.findByRole('button', { name: 'Criando…' })
    expect(botao).toHaveAttribute('aria-disabled', 'true')
    await r.usuario.type(campo(), '{Enter}')
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog', { name: 'Novo projeto' })).toBeInTheDocument()
    soltar()
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/projects/proj-2/'))
    expect(r.falsa.chamadas('POST /projects')).toBe(1)
  })

  // O item do menu some com o menu: sem a reserva, o foco cairia no <body>.
  it('Cancelar fecha e devolve o foco ao botão do seletor', async () => {
    const r = await renderizarPainel(`/projects/${PROJETO_PADRAO}/`)
    await abrirPeloSeletor(r)
    await r.usuario.click(screen.getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Projeto: Padrão' })).toHaveFocus())
  })
})

describe('sem projetos', () => {
  const semProjetos = (falsa: ApiFalsa) => {
    falsa.estado.projetos = []
  }

  // Sabotagem: a raiz com lista vazia na NaoEncontrada → "Página não
  // encontrada".
  it('a raiz mostra "Nenhum projeto", sem consulta de recurso', async () => {
    const r = await renderizarPainel('/', { antes: semProjetos })
    expect(await screen.findByRole('heading', { level: 1, name: 'Nenhum projeto' })).toBeInTheDocument()
    expect(
      screen.getByText('Alvos, canais e status pages ficam dentro de um projeto. Crie um para continuar.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Projeto: nenhum escolhido' })).toHaveTextContent('Escolher projeto')
    await waitFor(() => expect(document.title).toBe('Nenhum projeto — StillOn'))
    await act(() => new Promise((resolve) => setTimeout(resolve, 100)))
    expect(pedidosDeRecurso(r.falsa)).toEqual([])
  })

  it('"Criar projeto" abre o diálogo e o novo projeto vira o atual', async () => {
    const r = await renderizarPainel('/', { antes: semProjetos })
    await r.usuario.click(await screen.findByRole('button', { name: 'Criar projeto' }))
    const dialogo = await screen.findByRole('dialog', { name: 'Novo projeto' })
    await r.usuario.type(campo(), 'Clientes')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Criar projeto' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/projects/proj-1/'))
    expect(await screen.findByRole('button', { name: 'Projeto: Clientes' })).toBeInTheDocument()
  })

  it('o menu do seletor só tem "Novo projeto"', async () => {
    const r = await renderizarPainel('/', { antes: semProjetos })
    await screen.findByRole('heading', { level: 1, name: 'Nenhum projeto' })
    await r.usuario.click(screen.getByRole('button', { name: 'Projeto: nenhum escolhido' }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).queryAllByRole('menuitemradio')).toEqual([])
    expect(within(menu).getByRole('menuitem', { name: 'Novo projeto' })).toBeInTheDocument()
  })
})
