import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

const ITENS = [
  itemDoResumo({ id: 'b', name: 'Banco', state: 'down', tags: ['prod', 'dados'], open_incident_id: 'inc-1' }),
  itemDoResumo({ id: 'l', name: 'Lento', state: 'degraded', tags: ['prod'] }),
  itemDoResumo({ id: 'a', name: 'API', tags: ['prod', 'api'] }),
  itemDoResumo({ id: 's', name: 'Site', tags: ['web'] }),
  itemDoResumo({ id: 'p', name: 'Parado', state: 'unknown', enabled: false }),
]

async function preparar(rota = '/') {
  const r = await renderizarPainel(rota)
  r.falsa.estado.resumo = ITENS
  r.falsa.estado.incidentes = [incidente({ id: 'inc-1', target_id: 'b', target_name: 'Banco' })]
  await r.cliente.refetchQueries()
  await screen.findByRole('heading', { level: 2, name: 'Dois serviços pedem atenção.' })
  return r
}

const nomesNaTabela = () =>
  within(screen.getByRole('table', { name: /^Monitores/ }))
    .getAllByRole('rowheader')
    .map((c) => within(c).getByRole('link').textContent)
const filtro = (grupo: string, nome: RegExp) => within(screen.getByRole('group', { name: grupo })).getByRole('button', { name: nome })
const esperarNomes = (nomes: string[]) => waitFor(() => expect(nomesNaTabela()).toEqual(nomes))

describe('filtros do painel', () => {
  it('a barra mostra os estados e as tags, com a contagem do conjunto inteiro', async () => {
    await preparar()
    const estados = within(screen.getByRole('group', { name: 'Estado' })).getAllByRole('button')
    expect(estados.map((b) => b.textContent)).toEqual(['Fora do ar1', 'Degradado1', 'No ar2', 'Pausado1'])
    const tags = within(screen.getByRole('group', { name: 'Tags' })).getAllByRole('button')
    expect(tags.map((b) => b.textContent)).toEqual(['prod3', 'api1', 'dados1', 'web1'])
    for (const b of [...estados, ...tags]) expect(b).toHaveAttribute('aria-pressed', 'false')
  })

  // Sabotagem: não passar os filtros à consulta → a tabela não muda.
  it('filtrar por tag vai à API e reduz a tabela', async () => {
    const r = await preparar()
    await userEvent.click(filtro('Tags', /^prod/))
    await esperarNomes(['Banco', 'Lento', 'API'])
    expect(filtro('Tags', /^prod/)).toHaveAttribute('aria-pressed', 'true')
    expect(r.falsa.estado.consultasDoResumo).toContain('tag=prod')
    expect(screen.getByText('3 de 5 alvos')).toBeInTheDocument()
  })

  it('tags somam em E, e estado com tag restringe', async () => {
    await preparar()
    await userEvent.click(filtro('Tags', /^prod/))
    await esperarNomes(['Banco', 'Lento', 'API'])
    await userEvent.click(filtro('Tags', /^api/))
    await esperarNomes(['API'])
    await userEvent.click(filtro('Tags', /^api/))
    await esperarNomes(['Banco', 'Lento', 'API'])
    await userEvent.click(filtro('Estado', /^Fora do ar/))
    await esperarNomes(['Banco'])
    expect(filtro('Estado', /^Fora do ar/)).toHaveAttribute('aria-pressed', 'true')
  })

  // Critério 4 da spec. Sabotagem: alimentar a manchete com a lista filtrada.
  it('um filtro nunca esconde um incêndio: a manchete fala do conjunto inteiro', async () => {
    await preparar()
    await userEvent.click(filtro('Tags', /^web/))
    await esperarNomes(['Site'])
    expect(screen.getByRole('heading', { level: 2, name: 'Dois serviços pedem atenção.' })).toBeInTheDocument()
    expect(within(screen.getByRole('group', { name: 'Estado' })).getByRole('button', { name: /^Fora do ar/ })).toBeInTheDocument()
  })

  // Sabotagem: guardar os filtros em useState → o recarregamento os perde.
  it('os filtros vêm da URL', async () => {
    await preparar('/?tag=web&estado=up')
    await esperarNomes(['Site'])
    expect(filtro('Tags', /^web/)).toHaveAttribute('aria-pressed', 'true')
    expect(filtro('Estado', /^No ar/)).toHaveAttribute('aria-pressed', 'true')
  })

  // Sabotagem: mandar a busca à API → a contagem de consultas cresce a cada tecla.
  it('a busca é local, por nome e por tag, sem uma requisição por tecla', async () => {
    const r = await preparar()
    const antes = r.falsa.estado.consultasDoResumo.length
    await userEvent.type(screen.getByRole('searchbox', { name: 'Buscar alvo ou tag' }), 'dados')
    await esperarNomes(['Banco'])
    expect(r.falsa.estado.consultasDoResumo).toHaveLength(antes)
  })

  it('sem resultado: diz o que está ativo, some a tabela, fica a barra e limpar devolve tudo', async () => {
    await preparar('/?tag=web&estado=down')
    expect(await screen.findByText('Nenhum alvo com esses filtros.')).toBeInTheDocument()
    expect(screen.getByText('Tag web · Fora do ar')).toBeInTheDocument()
    expect(screen.queryByRole('table', { name: /^Monitores/ })).not.toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Tags' })).toBeInTheDocument()
    // Terciário (M6 da revisão, spec): nunca compete com o conteúdo.
    expect(screen.getByRole('button', { name: 'Limpar filtros' })).not.toHaveClass('border')
    expect(screen.getByRole('button', { name: 'Limpar filtros' })).toHaveClass('text-primaria')
    await userEvent.click(screen.getByRole('button', { name: 'Limpar filtros' }))
    await esperarNomes(['Banco', 'Lento', 'API', 'Site', 'Parado'])
    expect(screen.queryByRole('button', { name: 'Limpar filtros' })).not.toBeInTheDocument()
  })

  // Spec, Estados. Sabotagem: mostrar a tabela completa no erro → a falha
  // pareceria um filtro que não filtra.
  it('se a consulta filtrada falha, a manchete fica e a tabela vira o erro com nova tentativa', async () => {
    const r = await preparar()
    r.falsa.estado.forcar.set('GET /targets/summary?tag=web', { status: 502, code: 'unavailable' })
    await userEvent.click(filtro('Tags', /^web/))
    expect(await screen.findByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
    expect(screen.queryByRole('table', { name: /^Monitores/ })).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 2, name: 'Dois serviços pedem atenção.' })).toBeInTheDocument()
  })

  // D1 da revisão. O relógio da linha é o da resposta que ela mostra, não o da
  // consulta completa. Sabotagem: passar atualizadoEm à tabela filtrada → a
  // conta sai negativa e o limite vira "Check agora".
  it('a tabela filtrada conta o tempo a partir da própria resposta', async () => {
    const r = await preparar('/?tag=web')
    await esperarNomes(['Site'])
    r.falsa.estado.resumo = ITENS.map((item) => ({ ...item, last_checked_at: new Date(Date.now() - 5000).toISOString() }))
    r.cliente.setQueryData(['targets', 'summary'], r.cliente.getQueryData(['targets', 'summary']), { updatedAt: Date.now() - 60_000 })
    await r.cliente.refetchQueries({ predicate: (q) => q.queryKey.length > 2 && q.queryKey[1] === 'summary' })
    expect((await screen.findAllByText(/^Check há \d+ segundos$/)).length).toBeGreaterThan(0)
  })

  // M3 da revisão: enquanto a resposta não chega, a tabela de antes fica, com
  // aria-busy. Sabotagem: tirar o placeholderData → a tabela some.
  it('trocando o filtro, a tabela anterior fica ocupada até a resposta', async () => {
    const r = await preparar()
    let soltar!: () => void
    const segurado = new Promise<void>((ok) => (soltar = ok))
    servidor.use(
      http.get('*/api/v1/targets/summary', async ({ request }) => {
        if (!new URL(request.url).searchParams.has('tag')) return undefined
        await segurado
        return HttpResponse.json({ items: r.falsa.estado.resumo.filter((item) => item.tags.includes('web')) })
      }),
    )
    await userEvent.click(filtro('Tags', /^web/))
    const tabela = await screen.findByRole('table', { name: /^Monitores/ })
    expect(tabela.closest('[aria-busy]')).toHaveAttribute('aria-busy', 'true')
    expect(nomesNaTabela()).toEqual(['Banco', 'Lento', 'API', 'Site', 'Parado'])
    expect(screen.getByText('Filtrando…')).toBeInTheDocument()
    soltar()
    await esperarNomes(['Site'])
    expect(screen.getByRole('table', { name: /^Monitores/ }).closest('[aria-busy]')).toHaveAttribute('aria-busy', 'false')
    expect(screen.getByText('1 de 5 alvos')).toBeInTheDocument()
  })

  // D2 da revisão: o router aplica a URL numa transição; um campo controlado
  // por ela perde o cursor e pode perder tecla. Sabotagem: value={valor}
  // direto da URL → o X cai no fim do termo.
  it('a busca aceita edição no meio do termo, sem o cursor pular', async () => {
    await preparar()
    const campo = screen.getByRole<HTMLInputElement>('searchbox', { name: 'Buscar alvo ou tag' })
    await userEvent.type(campo, 'banco')
    await userEvent.type(campo, '{ArrowLeft}{ArrowLeft}Xa', { skipClick: true })
    expect(campo).toHaveValue('banXaco')
    expect(campo.selectionStart).toBe(5)
    await userEvent.type(campo, '{Backspace}{Backspace}', { skipClick: true })
    await esperarNomes(['Banco'])
    expect(campo).toHaveValue('banco')
  })

  it('digitar depressa não perde tecla', async () => {
    await preparar()
    const campo = screen.getByRole('searchbox', { name: 'Buscar alvo ou tag' })
    await userEvent.setup({ delay: null }).type(campo, 'parado')
    expect(campo).toHaveValue('parado')
    await esperarNomes(['Parado'])
  })

  // O campo espelha a URL quando ela muda por fora. Sabotagem: não
  // ressincronizar → o termo fica no campo depois de limpar.
  it('limpar os filtros esvazia o campo de busca', async () => {
    await preparar('/?busca=banco')
    const campo = screen.getByRole('searchbox', { name: 'Buscar alvo ou tag' })
    expect(campo).toHaveValue('banco')
    await userEvent.click(screen.getByRole('button', { name: 'Limpar filtros' }))
    await waitFor(() => expect(campo).toHaveValue(''))
  })

  // D4 da revisão: o botão clicado some, e o foco não pode cair no <body>.
  // Sabotagem: trocar BotaoQueSome por Botao.
  describe('o foco não cai no body', () => {
    const busca = () => screen.getByRole('searchbox', { name: 'Buscar alvo ou tag' })

    it('ao usar "Limpar filtros" da barra', async () => {
      await preparar('/?tag=web')
      await esperarNomes(['Site'])
      await userEvent.click(screen.getByRole('button', { name: 'Limpar filtros' }))
      await waitFor(() => expect(screen.queryByRole('button', { name: 'Limpar filtros' })).not.toBeInTheDocument())
      expect(busca()).toHaveFocus()
    })

    it('ao usar "Limpar filtros" do estado sem resultado', async () => {
      await preparar('/?tag=web&estado=down')
      await screen.findByText('Nenhum alvo com esses filtros.')
      await userEvent.click(screen.getByRole('button', { name: 'Limpar filtros' }))
      await esperarNomes(['Banco', 'Lento', 'API', 'Site', 'Parado'])
      expect(busca()).toHaveFocus()
    })

    it('ao desligar um estado com contagem zero', async () => {
      const r = await renderizarPainel('/?estado=down')
      r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API' })]
      await r.cliente.refetchQueries()
      await userEvent.click(await screen.findByRole('button', { name: /^Fora do ar/ }))
      await waitFor(() => expect(screen.queryByRole('button', { name: /^Fora do ar/ })).not.toBeInTheDocument())
      expect(busca()).toHaveFocus()
    })
  })

  it('sem nenhum alvo não há barra de filtros', async () => {
    await renderizarPainel('/')
    await screen.findByRole('heading', { level: 2, name: 'Nenhum serviço vigiado ainda.' })
    expect(screen.queryByRole('group', { name: 'Estado' })).not.toBeInTheDocument()
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  })

  it('sem nenhuma tag cadastrada, o grupo de tags não aparece', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo()]
    await r.cliente.refetchQueries()
    await screen.findByRole('table', { name: /^Monitores/ })
    expect(screen.queryByRole('group', { name: 'Tags' })).not.toBeInTheDocument()
  })

  it('a linha mostra até três tags e diz quantas faltam', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ tags: ['a', 'b', 'c', 'd', 'e'] })]
    await r.cliente.refetchQueries()
    const celula = within(await screen.findByRole('table', { name: /^Monitores/ })).getAllByRole('cell')[1]
    expect(celula).toHaveTextContent('abc+2')
    expect(within(celula as HTMLElement).getByText('mais d, e')).toHaveClass('sr-only')
  })
})
