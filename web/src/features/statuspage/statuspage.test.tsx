import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { sugerirSlug } from './PaginaStatusPage'

it('sugerirSlug tira acento e o que não cabe na regra', () => {
  expect(sugerirSlug('Situação da Ação — Produção!')).toBe('situacao-da-acao-producao')
  expect(sugerirSlug('  ')).toBe('')
})

describe('status page', () => {
  it('cria com alvos na ordem escolhida, e o slug ocupado cai no campo', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Site' })]
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Título'), 'Minha Empresa')
    await r.usuario.tab() // o blur sugere o slug
    expect(screen.getByLabelText('Endereço (slug)')).toHaveValue('minha-empresa')

    for (const nome of ['API', 'Site']) {
      await r.usuario.selectOptions(screen.getByLabelText('Acrescentar alvo'), nome)
      await r.usuario.click(screen.getByRole('button', { name: 'Acrescentar' }))
    }
    await r.usuario.click(screen.getByRole('button', { name: 'Subir Site' }))

    r.falsa.estado.forcar.set('POST /status-pages', { status: 409, code: 'slug_taken' })
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(screen.getByLabelText('Endereço (slug)')).toHaveAccessibleDescription(/já está em uso/))

    r.falsa.estado.forcar.delete('POST /status-pages')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(r.falsa.chamadas('POST /status-pages')).toBe(2))
    expect(r.falsa.corpos('POST /status-pages')[1]).toEqual({
      title: 'Minha Empresa',
      slug: 'minha-empresa',
      published: false,
      items: [
        { target_id: 'alvo-2', display_name: 'Site' },
        { target_id: 'alvo-1', display_name: 'API' },
      ],
    })
    // Rascunho: o link existe, mas a página ainda não abre.
    expect(await screen.findByText('A página só fica acessível depois de publicada.')).toBeInTheDocument()
  })

  it('a prévia é rotulada como aproximada', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [
      {
        id: 'pagina-1',
        slug: 'minha',
        title: 'Minha',
        published: true,
        items: [{ target_id: 'alvo-1', display_name: 'API pública' }],
        public_url: 'http://localhost:5173/p/minha',
        created_at: '2026-09-20T10:00:00Z',
      },
    ]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(within(previa).getByText('API pública')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Abrir' })).toHaveAttribute('rel', 'noopener noreferrer')
  })
  it('outra aba criou a página: o 409 status_page_exists recarrega, e o aviso sobrevive à troca do formulário', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Título'), 'Minha')
    await r.usuario.tab()
    // Enquanto isso, outra aba criou a página no servidor.
    r.falsa.estado.paginas = [salva()]
    r.falsa.estado.forcar.set('POST /status-pages', { status: 409, code: 'status_page_exists' })
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    expect(await screen.findByRole('button', { name: 'Salvar' })).toBeInTheDocument()
    expect(screen.getByLabelText('Título')).toHaveValue('Minha salva')
    expect(screen.getAllByText('Já existe uma status page; recarregamos a que está salva.').length).toBeGreaterThan(0)
  })

  it('editar manda no PATCH a lista inteira, reordenada e sem o item tirado', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Site' }), alvo({ id: 'alvo-3', name: 'Banco' })]
    r.falsa.estado.paginas = [
      salva({
        items: [
          { target_id: 'alvo-1', display_name: 'API' },
          { target_id: 'alvo-2', display_name: 'Site' },
          { target_id: 'alvo-3', display_name: 'Banco' },
        ],
      }),
    ]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Subir Banco' }))
    await r.usuario.click(screen.getByRole('button', { name: 'Tirar API da página' }))
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.chamadas('PATCH /status-pages/:id')).toBe(1))
    expect(r.falsa.corpos('PATCH /status-pages/:id')[0]).toEqual({
      title: 'Minha salva',
      slug: 'minha',
      published: true,
      items: [
        { target_id: 'alvo-3', display_name: 'Banco' },
        { target_id: 'alvo-2', display_name: 'Site' },
      ],
    })
  })

  // I1 da revisão final: o "Tirar" some com a linha. O foco vai ao "Tirar"
  // que ocupou o lugar, ao da última linha, ou ao seletor de acrescentar —
  // nunca ao <body>. Sabotagem: tirar o "focarDepoisDeTirar.current = i" →
  // o foco cai no body já no primeiro passo.
  it('tirar um alvo leva o foco ao próximo "Tirar", e não ao <body>', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Site' }), alvo({ id: 'alvo-3', name: 'Banco' })]
    r.falsa.estado.paginas = [
      salva({
        items: [
          { target_id: 'alvo-1', display_name: 'API' },
          { target_id: 'alvo-2', display_name: 'Site' },
          { target_id: 'alvo-3', display_name: 'Banco' },
        ],
      }),
    ]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Tirar API da página' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Tirar Site da página' })).toHaveFocus())
    await r.usuario.click(screen.getByRole('button', { name: 'Tirar Banco da página' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Tirar Site da página' })).toHaveFocus())
    await r.usuario.click(screen.getByRole('button', { name: 'Tirar Site da página' }))
    await waitFor(() => expect(screen.getByLabelText('Acrescentar alvo')).toHaveFocus())
  })

  // M3: a página sumiu entre carregar e salvar: um aviso que explica, e a
  // lista relida (o formulário volta a ser o de criar). Sabotagem: em
  // enviar(), tirar o ramo do naoEncontrado → "Não encontrado." no topo.
  it('salvar com 404 explica e recarrega', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.paginas = [salva()]
    await r.cliente.refetchQueries()
    await screen.findByRole('button', { name: 'Salvar' })
    r.falsa.estado.paginas = []
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(
      (await screen.findAllByText('A status page já tinha sido apagada, talvez em outra aba.'))[0],
    ).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Criar status page' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('o erro do servidor num item vai para o campo do item, sem o caminho cru no topo', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Site' })]
    r.falsa.estado.paginas = [
      salva({
        items: [
          { target_id: 'alvo-1', display_name: 'API' },
          { target_id: 'alvo-2', display_name: 'Site' },
        ],
      }),
    ]
    await r.cliente.refetchQueries()
    r.falsa.estado.forcar.set('PATCH /status-pages/:id', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'items[1].display_name', reason: 'required' }],
    })
    await r.usuario.click(await screen.findByRole('button', { name: 'Salvar' }))
    await waitFor(() =>
      expect(screen.getByLabelText('Nome de exibição de Site')).toHaveAttribute('aria-invalid', 'true'),
    )
    expect(screen.getByLabelText('Nome de exibição de API')).not.toHaveAttribute('aria-invalid')
    expect(screen.queryByText(/items\[1\]/)).not.toBeInTheDocument()
  })

  it('a sugestão curta demais não acusa erro no slug antes de a pessoa mexer nele', async () => {
    const r = await renderizarPainel('/status-pages')
    await r.usuario.type(await screen.findByLabelText('Título'), 'A')
    await r.usuario.tab()
    expect(screen.getByLabelText('Endereço (slug)')).toHaveValue('a')
    expect(screen.getByLabelText('Endereço (slug)')).not.toHaveAttribute('aria-invalid')
  })

  it('Copiar confirma só depois de copiar, e a falha vira aviso', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.paginas = [salva()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Copiar' }))
    expect(await screen.findByRole('button', { name: 'Copiado' })).toBeInTheDocument()
    expect(await navigator.clipboard.readText()).toBe('http://localhost:5173/p/minha')

    await waitFor(() => expect(screen.getByRole('button', { name: 'Copiar' })).toBeInTheDocument(), { timeout: 3000 })
    const falha = vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValueOnce(new Error('negado'))
    await r.usuario.click(screen.getByRole('button', { name: 'Copiar' }))
    expect((await screen.findAllByText('Não foi possível copiar. Selecione o link e copie à mão.')).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: 'Copiar' })).toBeInTheDocument()
    falha.mockRestore()
  }, 10_000) // espera de verdade os 2 s do rótulo voltar

  // M4 da revisão final: o feedback da cópia é um só (useCopiar): aviso no
  // sucesso e na falha; e uma falha logo depois de um sucesso devolve o
  // rótulo a "Copiar" na hora. Sabotagem: tirar o "setCopiado(false)" da
  // falha em useCopiar → o botão continua "Copiado".
  it('Copiar avisa o sucesso, e a falha logo depois volta o rótulo a "Copiar"', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.paginas = [salva()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Copiar' }))
    expect((await screen.findAllByText('Link copiado.'))[0]).toBeInTheDocument()
    const botao = screen.getByRole('button', { name: 'Copiado' })
    const falha = vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValueOnce(new Error('negado'))
    await r.usuario.click(botao)
    expect((await screen.findAllByText('Não foi possível copiar. Selecione o link e copie à mão.'))[0]).toBeInTheDocument()
    expect(botao).toHaveTextContent(/^Copiar$/)
    falha.mockRestore()
  })
})

function salva(p: Partial<{ items: { target_id: string; display_name: string }[] }> = {}) {
  return {
    id: 'pagina-1',
    slug: 'minha',
    title: 'Minha salva',
    published: true,
    items: [{ target_id: 'alvo-1', display_name: 'API pública' }],
    public_url: 'http://localhost:5173/p/minha',
    created_at: '2026-09-20T10:00:00Z',
    ...p,
  }
}
