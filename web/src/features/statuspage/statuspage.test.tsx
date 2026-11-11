import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { alvo, itemDoResumo, pagina } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'
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

  it('o formulário é um cartão de largura limitada', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [pagina()]
    await r.cliente.refetchQueries()
    const formulario = (await screen.findByLabelText('Título')).closest('form')
    expect(formulario).toHaveClass('max-w-2xl', 'rounded-padrao', 'bg-superficie', 'sombra-nivel-1')
  })

  // Auditoria SP3: a prévia parece a página pública: o título, a mesma faixa
  // de estado (faixa.ts) e um cartão por alvo. Sabotagem: tirar a faixa da
  // PreviaDaPagina → o teste não acha a frase.
  it('a prévia parece a página pública: título, faixa de estado e um cartão por alvo', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'down' })]
    r.falsa.estado.paginas = [pagina({ items: [{ target_id: 'alvo-1', display_name: 'API pública' }] })]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(within(previa).getByText('Minha')).toHaveClass('font-display')
    const frase = await within(previa).findByText('1 serviço fora do ar.')
    // A faixa tem a cor do pior estado, como na página pública.
    expect(frase.parentElement).toHaveClass('bg-fora-100', 'border-l-fora')
    expect(within(previa).getByText('Fora do ar: API pública.')).toBeInTheDocument()
    expect(within(previa).getByText('Fora do ar')).toHaveClass('rounded-full')
    await r.usuario.clear(screen.getByLabelText('Título'))
    await r.usuario.type(screen.getByLabelText('Título'), 'Loja')
    expect(within(previa).getByText('Loja')).toBeInTheDocument()
  })

  // A página pública não sabe de pausa: o alvo pausado sai "sem dados" na
  // faixa, como sairá na página publicada.
  it('na faixa da prévia, o alvo pausado conta como sem dados', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'up', enabled: false })]
    r.falsa.estado.paginas = [pagina({ items: [{ target_id: 'alvo-1', display_name: 'API pública' }] })]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    const frase = await within(previa).findByText('Sem dados recentes de 1 serviço.')
    expect(frase.parentElement).toHaveClass('bg-sem-dados-100', 'border-l-sem-dados')
  })

  // O alvo que não está no resumo (apagado entre as duas leituras) também
  // sai "sem dados" na faixa: a página pública não o teria no ar.
  it('na faixa da prévia, o alvo fora do resumo conta como sem dados', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Banco' })]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'up' })]
    r.falsa.estado.paginas = [
      pagina({
        items: [
          { target_id: 'alvo-1', display_name: 'API' },
          { target_id: 'alvo-2', display_name: 'Banco' },
        ],
      }),
    ]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(await within(previa).findByText('Sem dados recentes de 1 serviço.')).toBeInTheDocument()
    expect(within(previa).getByText('Sem dados: Banco.')).toBeInTheDocument()
  })

  // Sem o resumo, a prévia não sabe o estado de ninguém: carregando, a faixa
  // não aparece; com erro, ela diz isso, em vez de "sem dados recentes".
  it('enquanto o resumo carrega, a faixa da prévia não afirma estado nenhum', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.resumo = [itemDoResumo({ state: 'down' })]
    r.falsa.estado.paginas = [pagina()]
    await r.cliente.refetchQueries()
    let soltar!: () => void
    const liberada = new Promise<void>((resolve) => {
      soltar = resolve
    })
    servidor.use(
      http.get(
        '*/api/v1/targets/summary',
        async () => {
          await liberada
          return HttpResponse.json({ items: [itemDoResumo({ state: 'down' })] })
        },
      ),
    )
    const leitura = r.cliente.resetQueries({ queryKey: ['targets', 'summary'] })
    try {
      const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
      await waitFor(() => expect(r.cliente.getQueryState(['targets', 'summary'])?.status).toBe('pending'))
      expect(within(previa).queryByText(/Sem dados recentes|fora do ar\.|Todos os sistemas/)).not.toBeInTheDocument()
    } finally {
      soltar()
      await leitura
    }
    const previa = screen.getByRole('region', { name: 'Prévia aproximada' })
    expect(await within(previa).findByText('1 serviço fora do ar.')).toBeInTheDocument()
  })

  it('se o resumo falha, a faixa da prévia diz que o estado não carregou', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [pagina()]
    r.falsa.estado.forcar.set('GET /targets/summary', { status: 503, code: 'unavailable' })
    await r.cliente.resetQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    const aviso = await within(previa).findByText('Não foi possível carregar o estado dos alvos.')
    expect(aviso.parentElement).toHaveClass('bg-painel')
    expect(within(previa).queryByText(/Sem dados recentes/)).not.toBeInTheDocument()
  })

  // Auditoria SP2: reordenar é secundário, em ícone; Tirar, que remove, é
  // terciário. Os nomes acessíveis de antes ficam.
  it('Subir e Descer são botões de ícone secundários, e Tirar é terciário', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Banco' })]
    r.falsa.estado.paginas = [
      pagina({
        items: [
          { target_id: 'alvo-1', display_name: 'API' },
          { target_id: 'alvo-2', display_name: 'Banco' },
        ],
      }),
    ]
    await r.cliente.refetchQueries()
    for (const nome of ['Subir Banco', 'Descer API']) {
      const botao = await screen.findByRole('button', { name: nome })
      expect(botao).toHaveClass('border-borda-controle', 'size-11')
      expect(botao.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    }
    const tirar = screen.getByRole('button', { name: 'Tirar API da página' })
    expect(tirar).toHaveClass('text-primaria')
    expect(tirar).not.toHaveClass('border-borda-controle')
  })

  it('o link público é um cartão de largura limitada', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [pagina()]
    await r.cliente.refetchQueries()
    const rotulo = await screen.findByText('Link público')
    expect(rotulo.parentElement).toHaveClass('max-w-2xl', 'rounded-padrao', 'bg-superficie')
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
    // Informa, e não é erro: não fica preso na tela à espera de um "X".
    // Sabotagem: marcar o aviso como erro.
    expect(screen.queryByRole('button', { name: 'Dispensar aviso' })).not.toBeInTheDocument()
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
    expect(screen.queryByRole('button', { name: 'Dispensar aviso' })).not.toBeInTheDocument()
  })

  // Como o I1: "Descer" (ou "Subir") que leva o item à ponta ficava
  // disabled, e o botão desabilitado com o foco o perde para o <body> no
  // navegador. Agora é aria-disabled, com a guarda no clique. Sabotagem:
  // voltar a "disabled={i === itens.fields.length - 1}" no Descer → o botão
  // não tem aria-disabled (e, no navegador, o foco cai).
  it('Descer até a ponta mantém o foco no botão, que fica aria-disabled e não mexe mais', async () => {
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
    const descer = await screen.findByRole('button', { name: 'Descer API' })
    await r.usuario.click(descer)
    await waitFor(() => expect(descer).toHaveAttribute('aria-disabled', 'true'))
    expect(descer).toHaveFocus()
    expect(descer).toBeEnabled()
    // Na ponta, o clique não faz nada: a ordem continua Site, API.
    await r.usuario.click(descer)
    const nomes = screen.getAllByLabelText(/^Nome de exibição de/).map((e) => (e as HTMLInputElement).value)
    expect(nomes).toEqual(['Site', 'API'])
    expect(screen.getByRole('button', { name: 'Subir Site' })).toHaveAttribute('aria-disabled', 'true')
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
    // Ao contrário do alvo e do heartbeat, esta tela não leva o foco ao campo com erro.
    expect(screen.getByLabelText('Nome de exibição de Site')).not.toHaveFocus()
  })

  // Um campo que a tela não sabe marcar não some: a mensagem vai ao erro do
  // topo. Sabotagem: tirar o ramo "campo === NO_TOPO" do catch.
  it('o 400 com um campo que a tela não tem mostra a mensagem no erro do topo', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [salva()]
    await r.cliente.refetchQueries()
    r.falsa.estado.forcar.set('PATCH /status-pages/:id', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'owner_id', reason: 'forbidden' }],
    })
    await r.usuario.click(await screen.findByRole('button', { name: 'Salvar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Não permitido.')
    expect(screen.queryByText(/owner_id/)).not.toBeInTheDocument()
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
