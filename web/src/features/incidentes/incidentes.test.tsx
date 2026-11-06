import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, onTestFinished, vi } from 'vitest'
import { incidente } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

describe('incidentes', () => {
  it('lista os abertos por padrão, com causa em texto, e reconhece', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [
      incidente(),
      incidente({ id: 'inc-2', resolved_at: '2026-09-24T11:30:00Z', target_name: 'Site' }),
    ]
    await r.cliente.refetchQueries()
    const lista = (await screen.findByText(/Tempo esgotado/)).closest('ul') as HTMLElement
    expect(within(lista).getAllByRole('listitem')).toHaveLength(1)
    expect(within(lista).getByText(/Tempo esgotado/)).toBeInTheDocument()
    // O que o botão faz está num texto visível, e não só num title.
    expect(screen.getByRole('button', { name: 'Reconhecer o incidente de API' })).toHaveAccessibleDescription(
      'Reconhecer interrompe os lembretes; não resolve o incidente.',
    )
    await r.usuario.click(screen.getByRole('button', { name: 'Reconhecer o incidente de API' }))
    expect(await screen.findByText(/Reconhecido em/)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
  })

  it('o filtro vai para a URL e muda a consulta', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente({ resolved_at: '2026-09-24T11:30:00Z', target_name: 'Site' })]
    await r.usuario.click(await screen.findByRole('radio', { name: 'Resolvidos' }))
    expect(r.router.state.location.search).toBe('?status=resolved')
    expect(await screen.findByText(/durou 30 min/)).toBeInTheDocument()
    // Resolvido não se reconhece.
    expect(screen.queryByRole('button', { name: /Reconhecer/ })).not.toBeInTheDocument()
  })

  it('"Carregar mais" segue o next_cursor', async () => {
    const r = await renderizarPainel('/incidents?status=all')
    r.falsa.estado.incidentes = Array.from({ length: 60 }, (_, i) =>
      incidente({ id: `inc-${String(i).padStart(2, '0')}`, target_name: `Alvo ${i}` }),
    )
    await r.cliente.refetchQueries()
    const itens = () => screen.getAllByRole('listitem').filter((li) => li.textContent?.includes('Tempo esgotado'))
    await waitFor(() => expect(itens()).toHaveLength(50))
    await r.usuario.click(screen.getByRole('button', { name: 'Carregar mais' }))
    await waitFor(() => expect(itens()).toHaveLength(60))
    expect(screen.queryByRole('button', { name: 'Carregar mais' })).not.toBeInTheDocument()
  })

  // WCAG 2.4.3: o botão fica ocupado enquanto a página chega e some na última;
  // sem um destino, o foco cairia no <body> e o teclado voltaria ao topo.
  describe('foco do "Carregar mais"', () => {
    // Por texto, e não por papel: o getByRole calcula o nome de cada elemento
    // dos 60 cartões, e num waitFor isso estoura o prazo com a máquina ocupada.
    const botao = () => screen.getByText('Carregar mais', { selector: 'button' })
    const linkDe = (nome: string) => screen.getByText(nome, { selector: 'a' })
    const itens = () => screen.getAllByText(/Tempo esgotado/)

    async function listaCom(quantos: number) {
      const r = await renderizarPainel('/incidents?status=all')
      r.falsa.estado.incidentes = Array.from({ length: quantos }, (_, i) =>
        incidente({ id: `inc-${String(i).padStart(3, '0')}`, target_name: `Alvo ${i}` }),
      )
      await r.cliente.refetchQueries()
      await waitFor(() => expect(itens()).toHaveLength(Math.min(quantos, 50)))
      return r
    }

    // Segura a próxima página até o teste soltar: só os GETs com cursor, e
    // não os do poll nem os do contador da navegação.
    function segurarProximaPagina() {
      let soltar = () => {}
      const preso = new Promise<void>((ok) => {
        soltar = ok
      })
      const pedidos = { total: 0 }
      servidor.use(
        http.get('*/api/v1/incidents', async ({ request }) => {
          if (!new URL(request.url).searchParams.has('cursor')) return
          pedidos.total++
          await preso
        }),
      )
      return { soltar: () => soltar(), pedidos }
    }

    it('na última página, o botão some e o foco vai ao primeiro item novo', async () => {
      const r = await listaCom(60)
      await r.usuario.click(botao())
      await waitFor(() => expect(itens()).toHaveLength(60))
      await waitFor(() => expect(linkDe('Alvo 50')).toHaveFocus())
      expect(screen.queryByText('Carregar mais')).not.toBeInTheDocument()
    })

    it('com mais páginas, o botão fica e o foco vai ao primeiro item novo', async () => {
      const r = await listaCom(120)
      await r.usuario.click(botao())
      await waitFor(() => expect(itens()).toHaveLength(100))
      await waitFor(() => expect(linkDe('Alvo 50')).toHaveFocus())
      expect(botao()).toBeInTheDocument()
    })

    it('funciona pelo teclado, a partir do botão', async () => {
      const r = await listaCom(60)
      botao().focus()
      await r.usuario.keyboard('{Enter}')
      await waitFor(() => expect(itens()).toHaveLength(60))
      await waitFor(() => expect(linkDe('Alvo 50')).toHaveFocus())
    })

    // O servidor ordena por data, e a tela agrupa por situação: um aberto
    // antigo entra no primeiro grupo, antes dos resolvidos já na tela.
    it('o primeiro item novo é o da ordem da tela, não o da página recebida', async () => {
      const r = await renderizarPainel('/incidents?status=all')
      const resolvido = { resolved_at: '2026-09-24T11:30:00Z' }
      r.falsa.estado.incidentes = [
        ...Array.from({ length: 50 }, (_, i) => incidente({ id: `inc-${i}`, target_name: `Antigo ${i}`, ...resolvido })),
        incidente({ id: 'inc-resolvido', target_name: 'Novo resolvido', ...resolvido }),
        incidente({ id: 'inc-aberto', target_name: 'Novo aberto' }),
      ]
      await r.cliente.refetchQueries()
      await r.usuario.click(await screen.findByText('Carregar mais', { selector: 'button' }))
      await waitFor(() => expect(linkDe('Novo aberto')).toHaveFocus())
    })

    // O servidor manda next_cursor sempre que a página vem cheia: com
    // exatamente 50 itens, a seguinte vem vazia e o botão some sem item novo.
    // A API falsa não faz isso, daí o handler próprio.
    it('se a página seguinte vier vazia, o foco fica no último item da lista', async () => {
      const r = await renderizarPainel('/incidents?status=all')
      const pagina = Array.from({ length: 50 }, (_, i) => incidente({ id: `inc-${i}`, target_name: `Alvo ${i}` }))
      servidor.use(
        http.get('*/api/v1/incidents', ({ request }) =>
          new URL(request.url).searchParams.has('cursor')
            ? HttpResponse.json({ items: [] })
            : HttpResponse.json({ items: pagina, next_cursor: 'inc-49' }),
        ),
      )
      await r.cliente.refetchQueries()
      await r.usuario.click(await screen.findByText('Carregar mais', { selector: 'button' }))
      await waitFor(() => expect(screen.queryByText('Carregar mais')).not.toBeInTheDocument())
      await waitFor(() => expect(linkDe('Alvo 49')).toHaveFocus())
    })

    it('enquanto a página chega, o botão fica ocupado sem perder o foco nem pedir de novo', async () => {
      const r = await listaCom(60)
      const { soltar, pedidos } = segurarProximaPagina()
      await r.usuario.click(botao())
      await waitFor(() => expect(botao()).toHaveAttribute('aria-disabled', 'true'))
      expect(botao()).toBeEnabled()
      expect(botao()).toHaveFocus()
      await r.usuario.click(botao())
      soltar()
      await waitFor(() => expect(itens()).toHaveLength(60))
      expect(pedidos.total).toBe(1)
      await waitFor(() => expect(linkDe('Alvo 50')).toHaveFocus())
    })

    it('uma falha mantém o foco no botão, que volta a funcionar', async () => {
      const r = await listaCom(60)
      r.falsa.estado.forcar.set('GET /incidents', { status: 502, code: 'unavailable' })
      await r.usuario.click(botao())
      expect(await screen.findByText(/Não foi possível atualizar agora/)).toBeInTheDocument()
      expect(botao()).toHaveFocus()
      expect(botao()).not.toHaveAttribute('aria-disabled')
    })

    it('não rouba o foco de quem foi para outro lugar enquanto a página chegava', async () => {
      const r = await listaCom(60)
      const { soltar } = segurarProximaPagina()
      await r.usuario.click(botao())
      await waitFor(() => expect(botao()).toHaveAttribute('aria-disabled', 'true'))
      const filtro = screen.getByRole('radio', { name: 'Todos' })
      filtro.focus()
      soltar()
      await waitFor(() => expect(itens()).toHaveLength(60))
      // Um tique real: um foco atrasado chegaria depois da lista.
      await new Promise((res) => setTimeout(res, 50))
      expect(filtro).toHaveFocus()
    })
  })

  // E10 da revisão de 2026-10-14: o poll de 30 s de uma infinite query refaz
  // TODAS as páginas carregadas, em série, e gasta o rate limit por usuário.
  // O maxPages do TanStack Query v5 exige lista bidirecional (o cursor daqui
  // só anda para frente) e descartaria as páginas de cima. Então o poll só
  // roda com uma página; com mais, a lista se atualiza no foco e no
  // reconhecer. Sabotagens: voltar a "refetchInterval: 30_000" → 2 GETs no
  // tique; "refetchInterval: false" → nenhum GET com uma página só.
  describe('poll da lista de incidentes', () => {
    // Só o setInterval é falso: o MSW e o React seguem no relógio real.
    beforeEach(() => vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] }))
    afterEach(() => vi.useRealTimers())

    // Os GETs da lista da página (sem limit), não os do contador da navegação.
    function contarGetsDaLista() {
      const contagem = { valor: 0 }
      const ouvir = ({ request }: { request: Request }) => {
        const url = new URL(request.url)
        if (request.method === 'GET' && url.pathname.endsWith('/incidents') && !url.searchParams.has('limit')) {
          contagem.valor++
        }
      }
      servidor.events.on('request:start', ouvir)
      onTestFinished(() => servidor.events.removeListener('request:start', ouvir))
      return contagem
    }

    async function listaCom(quantos: number) {
      const r = await renderizarPainel('/incidents?status=all')
      r.falsa.estado.incidentes = Array.from({ length: quantos }, (_, i) =>
        incidente({ id: `inc-${String(i).padStart(2, '0')}`, target_name: `Alvo ${i}` }),
      )
      await r.cliente.refetchQueries()
      return r
    }
    const itens = () => screen.getAllByRole('listitem').filter((li) => li.textContent?.includes('Tempo esgotado'))

    it('com uma página, o poll de 30 s refaz essa página', async () => {
      await listaCom(10)
      await waitFor(() => expect(itens()).toHaveLength(10))
      const gets = contarGetsDaLista()
      act(() => vi.advanceTimersByTime(30_000))
      // O waitFor sonda com setInterval, que aqui é falso: espera real.
      await new Promise((res) => setTimeout(res, 50))
      expect(gets.valor).toBe(1)
    })

    it('com mais de uma página carregada, o poll não refaz a lista inteira', async () => {
      const r = await listaCom(60)
      await waitFor(() => expect(itens()).toHaveLength(50))
      await r.usuario.click(screen.getByRole('button', { name: 'Carregar mais' }))
      await waitFor(() => expect(itens()).toHaveLength(60))
      const gets = contarGetsDaLista()
      act(() => vi.advanceTimersByTime(30_000))
      // Um tique real para um GET agendado chegar ao MSW, se fosse sair.
      await new Promise((res) => setTimeout(res, 50))
      expect(gets.valor).toBe(0)
      expect(itens()).toHaveLength(60)
    })
  })

  // D17: uma página seguinte (ou um poll) que falha não apaga o que já está na tela.
  it('"Carregar mais" que falha mantém a lista e avisa que os dados estão velhos', async () => {
    const r = await renderizarPainel('/incidents?status=all')
    r.falsa.estado.incidentes = Array.from({ length: 60 }, (_, i) =>
      incidente({ id: `inc-${String(i).padStart(2, '0')}`, target_name: `Alvo ${i}` }),
    )
    await r.cliente.refetchQueries()
    const itens = () => screen.getAllByRole('listitem').filter((li) => li.textContent?.includes('Tempo esgotado'))
    await waitFor(() => expect(itens()).toHaveLength(50))
    r.falsa.estado.forcar.set('GET /incidents', { status: 502, code: 'unavailable' })
    await r.usuario.click(screen.getByRole('button', { name: 'Carregar mais' }))
    expect(await screen.findByText(/Não foi possível atualizar agora/)).toBeInTheDocument()
    expect(itens()).toHaveLength(50)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('um ack que falha mostra o erro, e trocar o filtro o limpa', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    r.falsa.estado.forcar.set('POST /incidents/:id/ack', { status: 502, code: 'unavailable' })
    const antes = r.falsa.chamadas('GET /incidents')
    await r.usuario.click(await screen.findByRole('button', { name: 'Reconhecer o incidente de API' }))
    const erro = await screen.findByRole('alert')
    // Mesmo com a falha a lista é reconsultada: o ack pode ter chegado ao servidor.
    await waitFor(() => expect(r.falsa.chamadas('GET /incidents')).toBeGreaterThan(antes))
    expect(screen.getByRole('button', { name: 'Reconhecer o incidente de API' })).toBeEnabled()
    await r.usuario.click(screen.getByRole('radio', { name: 'Todos' }))
    await waitFor(() => expect(erro).not.toBeInTheDocument())
  })

  it('sem incidentes em "Todos", a frase não fica com espaço solto', async () => {
    await renderizarPainel('/incidents?status=all')
    expect(await screen.findByText('Nenhum incidente.')).toBeInTheDocument()
  })

  it('Reconhecer fica desabilitado enquanto o POST está em voo', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    let soltar!: () => void
    const segurado = new Promise<void>((ok) => (soltar = ok))
    // Segura o ack e depois cai no handler da API falsa (sem retorno, o MSW segue adiante).
    servidor.use(
      http.post('*/api/v1/incidents/:id/ack', async () => {
        await segurado
      }),
    )
    const botao = await screen.findByRole('button', { name: 'Reconhecer o incidente de API' })
    await r.usuario.click(botao)
    // aria-disabled, e não disabled: o foco fica no botão enquanto o POST voa.
    await waitFor(() => expect(botao).toHaveAttribute('aria-disabled', 'true'))
    expect(botao).toHaveFocus()
    await r.usuario.click(botao)
    soltar()
    expect(await screen.findByText(/Reconhecido em/)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
  })

  // I1 da revisão final: o Reconhecer some com o ack; o foco vai ao
  // "Reconhecido em" da linha, e não ao <body>. Sabotagem: tirar o
  // "focarDepois.current = id" de reconhecer() → o foco cai no body.
  it('depois de reconhecer, o foco vai ao "Reconhecido em", e não ao <body>', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Reconhecer o incidente de API' }))
    const feito = await screen.findByText(/Reconhecido em/)
    await waitFor(() => expect(feito).toHaveFocus())
  })

  // M3: o incidente sumiu (o alvo foi apagado em outra aba): um aviso que
  // explica, e a lista relida — sem o "Não encontrado." cru. Sabotagem: em
  // reconhecer(), tirar o ramo do naoEncontrado → o alert aparece.
  it('ack com 404 explica e relê a lista', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente()]
    await r.cliente.refetchQueries()
    const botao = await screen.findByRole('button', { name: 'Reconhecer o incidente de API' })
    r.falsa.estado.incidentes = []
    await r.usuario.click(botao)
    expect(
      (await screen.findAllByText('O incidente não existe mais: o alvo foi apagado, talvez em outra aba.'))[0],
    ).toBeInTheDocument()
    expect(await screen.findByText('Nenhum incidente aberto.')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  describe('redesign da lista', () => {
    it('o filtro é um radiogroup: a seta escolhe o próximo e vai para a URL', async () => {
      const r = await renderizarPainel('/incidents')
      const grupo = await screen.findByRole('radiogroup', { name: 'Filtro de incidentes' })
      expect(within(grupo).getByRole('radio', { name: 'Abertos' })).toBeChecked()
      await r.usuario.click(within(grupo).getByRole('radio', { name: 'Abertos' }))
      await r.usuario.keyboard('{ArrowRight}')
      expect(r.router.state.location.search).toBe('?status=resolved')
      expect(within(grupo).getByRole('radio', { name: 'Resolvidos' })).toBeChecked()
    })

    // Spec, Telas > 3: a frase de resumo vem dos abertos, em qualquer filtro.
    it('o resumo conta os abertos em andamento e os reconhecidos, mesmo no filtro de resolvidos', async () => {
      const r = await renderizarPainel('/incidents?status=resolved')
      r.falsa.estado.incidentes = [
        incidente(),
        incidente({ id: 'inc-2', target_name: 'Site', acknowledged_at: '2026-09-24T11:10:00Z' }),
        incidente({ id: 'inc-3', resolved_at: '2026-09-24T11:30:00Z' }),
      ]
      await r.cliente.refetchQueries()
      expect(await screen.findByText('1 em andamento, 1 reconhecido.')).toBeInTheDocument()
    })

    // O servidor dá next_cursor com a página cheia: com exatamente o limite, a
    // tela não sabe se há mais. Sabotagem: a falsa só dar cursor se sobrar item.
    it('com exatamente 100 abertos, o resumo diz "ou mais"', async () => {
      const r = await renderizarPainel('/incidents?status=resolved')
      r.falsa.estado.incidentes = Array.from({ length: 100 }, (_, i) => incidente({ id: `inc-${i}` }))
      await r.cliente.refetchQueries()
      expect(await screen.findByText('100 ou mais incidentes abertos.')).toBeInTheDocument()
    })

    // I4 da auditoria: o vazio dos abertos é o estado bom. Sabotagem: voltar
    // ao parágrafo "Nenhum incidente aberto." sem o ícone.
    it('sem abertos, o vazio é positivo: check em círculo verde, sem frase de resumo', async () => {
      await renderizarPainel('/incidents')
      const frase = await screen.findByText('Nenhum incidente aberto.')
      expect(frase.previousElementSibling).toHaveClass('bg-no-ar-100')
      expect(screen.queryByText(/em andamento[,.]/)).not.toBeInTheDocument()
    })

    it('em Todos, os grupos separam em andamento e resolvidos, e cada cartão mostra a situação', async () => {
      const r = await renderizarPainel('/incidents?status=all')
      r.falsa.estado.incidentes = [
        incidente({ id: 'a', target_name: 'API' }),
        incidente({ id: 'b', target_name: 'Site', acknowledged_at: '2026-09-24T11:10:00Z' }),
        incidente({ id: 'c', target_name: 'Banco', resolved_at: '2026-09-24T11:30:00Z' }),
      ]
      await r.cliente.refetchQueries()
      const andamento = await screen.findByRole('region', { name: 'Em andamento' })
      const resolvidos = screen.getByRole('region', { name: 'Resolvidos recentes' })
      const cartao = (regiao: HTMLElement, nome: string) => within(regiao).getByRole('link', { name: nome }).closest('li') as HTMLElement
      expect(within(andamento).getAllByRole('listitem')).toHaveLength(2)

      expect(cartao(andamento, 'API')).toHaveClass('border-l-fora')
      expect(within(cartao(andamento, 'API')).getByText('Em andamento')).toBeInTheDocument()
      expect(within(cartao(andamento, 'API')).getByRole('button', { name: 'Reconhecer o incidente de API' })).toBeInTheDocument()

      expect(cartao(andamento, 'Site')).toHaveClass('border-l-primaria')
      expect(within(cartao(andamento, 'Site')).getByText(/· lembretes parados$/)).toBeInTheDocument()
      expect(within(cartao(andamento, 'Site')).queryByRole('button')).not.toBeInTheDocument()

      // A borda de 4 px também no resolvido, na cor da borda: sem ela, o texto
      // começaria 3 px à esquerda dos cartões abertos.
      expect(cartao(resolvidos, 'Banco')).toHaveClass('border-l-4', 'border-l-borda')
      expect(within(resolvidos).getByRole('link', { name: 'Banco' })).toHaveClass('text-apagado')
      expect(within(cartao(resolvidos, 'Banco')).getByText('Resolvido')).toBeInTheDocument()
    })

    // O nome do alvo é o título do cartão, como no painel: o leitor de tela
    // navega pelos títulos. Sabotagem: tirar o <h3> em volta do link.
    it('o nome do alvo de cada cartão é um título de nível 3 com o link', async () => {
      const r = await renderizarPainel('/incidents?status=all')
      r.falsa.estado.incidentes = [
        incidente({ id: 'a', target_name: 'API' }),
        incidente({ id: 'c', target_name: 'Banco', resolved_at: '2026-09-24T11:30:00Z' }),
      ]
      await r.cliente.refetchQueries()
      for (const nome of ['API', 'Banco']) {
        const titulo = await screen.findByRole('heading', { level: 3, name: nome })
        expect(within(titulo).getByRole('link', { name: nome })).toBeInTheDocument()
      }
    })

    // Review Focus 7.
    it('incidente sem causa diz que a causa não foi registrada', async () => {
      const r = await renderizarPainel('/incidents')
      r.falsa.estado.incidentes = [incidente({ cause: '' })]
      await r.cliente.refetchQueries()
      expect(await screen.findByText(/^Causa não registrada · início /)).toBeInTheDocument()
    })
  })
})
