import { screen, waitFor, within } from '@testing-library/react'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
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
      /marca que você está ciente/,
    )
    await r.usuario.click(screen.getByRole('button', { name: 'Reconhecer o incidente de API' }))
    expect(await screen.findByText(/Reconhecido em/)).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /incidents/:id/ack')).toBe(1)
  })

  it('o filtro vai para a URL e muda a consulta', async () => {
    const r = await renderizarPainel('/incidents')
    r.falsa.estado.incidentes = [incidente({ resolved_at: '2026-09-24T11:30:00Z', target_name: 'Site' })]
    await r.usuario.click(await screen.findByRole('button', { name: 'Resolvidos' }))
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
    await r.usuario.click(screen.getByRole('button', { name: 'Todos' }))
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
})

