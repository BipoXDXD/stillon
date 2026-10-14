import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { canal, incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'
import { ItemDoResumo } from '@/lib/api/schemas'
import { mapaDeEstados, transicoes } from './transicoes'

const resumido = (p: Parameters<typeof itemDoResumo>[0]) => ItemDoResumo.parse(itemDoResumo(p))
const regiao = (nome: string) => screen.getByRole('region', { name: nome })
const cartaoDe = async (nome: string) =>
  (await screen.findByRole('heading', { level: 3, name: nome })).closest('li') as HTMLElement

describe('transicoes', () => {
  // Sabotagem prevista: anunciar todo item a cada resposta (ignorar o
  // "antes !== agora") → o segundo caso passa a devolver frases.
  it('só anuncia mudança: nada na primeira carga, nada sem mudança', () => {
    const a = [resumido({ id: '1', name: 'API', state: 'up' })]
    expect(transicoes(null, a)).toEqual([])
    expect(transicoes(mapaDeEstados(a), a)).toEqual([])
    const b = [resumido({ id: '1', name: 'API', state: 'down' })]
    expect(transicoes(mapaDeEstados(a), b)).toEqual(['API: fora do ar'])
    expect(transicoes(mapaDeEstados(b), a)).toEqual(['API: voltou ao ar'])
    const pausado = [resumido({ id: '1', name: 'API', state: 'unknown', enabled: false })]
    expect(transicoes(mapaDeEstados(a), pausado)).toEqual(['API: pausado'])
  })
})

describe('painel', () => {
  // Review Focus 1.
  it('sem alvos, a manchete diz que não há o que vigiar e oferece cadastrar o primeiro', async () => {
    await renderizarPainel('/')
    expect(await screen.findByRole('heading', { level: 2, name: 'Nenhum serviço vigiado ainda.' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Cadastrar o primeiro alvo' })).toHaveAttribute('href', '/targets/new')
    expect(screen.queryByText(/^Atualizado às/)).not.toBeInTheDocument()
  })

  // Spec, Telas > 1: problemas primeiro, em cartões; o que funciona, em
  // linhas com o cabeçalho uma vez só; sem dados e pausados, em texto, sem
  // gráfico (auditoria P7). Review Focus 4 (o "Novo" nunca foi verificado).
  it('ordena por gravidade: problemas em cartões, no ar em linhas, sem dados e pausados em texto', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'z', name: 'Zeta' }),
      itemDoResumo({ id: 'a', name: 'alfa', uptime_24h: 0.99999 }),
      itemDoResumo({ id: 'l', name: 'Lento', state: 'degraded', last_latency_ms: 2400 }),
      itemDoResumo({ id: 'c', name: 'Caiu', state: 'down', last_latency_ms: null }),
      itemDoResumo({ id: 'b', name: 'Banco', state: 'down', last_latency_ms: null }),
      itemDoResumo({ id: 'p', name: 'Parado', state: 'unknown', enabled: false }),
      itemDoResumo({ id: 'n', name: 'Novo', state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] }),
    ]
    await r.cliente.refetchQueries()
    const problemas = await screen.findByRole('region', { name: 'Pedem atenção' })
    expect(within(problemas).getAllByRole('heading', { level: 3 }).map((h) => h.textContent)).toEqual(['Banco', 'Caiu', 'Lento'])
    expect(within(problemas).getAllByText('Fora do ar')).toHaveLength(2)
    expect(within(problemas).getByText('Degradado')).toBeInTheDocument()

    const funcionando = regiao('Funcionando')
    expect(within(funcionando).getAllByRole('columnheader').map((c) => c.textContent)).toEqual([
      'Alvo',
      'Latência',
      '24 h',
      'Últimas 24 h',
    ])
    expect(within(funcionando).getAllByRole('rowheader').map((c) => c.textContent)).toEqual(['alfa', 'Zeta'])
    // Truncado: 0,99999 é "99,99%", nunca "100,00%".
    expect(within(funcionando).getByRole('row', { name: /alfa/ })).toHaveTextContent('99,99%')

    const semDados = regiao('Sem dados ou pausados')
    expect(
      within(semDados)
        .getAllByRole('listitem')
        .map((li) => [within(li).getByRole('link').textContent, li.querySelector('span')?.textContent]),
    ).toEqual([
      ['Novo', 'Sem dados · nunca verificado'],
      ['Parado', 'Pausado'],
    ])
    expect(within(semDados).queryByRole('img')).not.toBeInTheDocument()
  })

  it('a manchete conta os problemas por extenso e detalha os estados por gravidade', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'b', name: 'Banco', state: 'down' }),
      itemDoResumo({ id: 'l', name: 'Lento', state: 'degraded' }),
      itemDoResumo({ id: 'a', name: 'API' }),
      itemDoResumo({ id: 's', name: 'Site' }),
    ]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { level: 2, name: 'Dois serviços pedem atenção.' })).toBeInTheDocument()
    expect(screen.getByText('1 fora do ar, 1 degradado, 2 no ar.')).toBeInTheDocument()
    expect(screen.getByText(/^Atualizado às \d{2}:\d{2} · 2 de 4 no ar$/)).toBeInTheDocument()
  })

  // Review Focus 2.
  it('todos pausados: a manchete diz, e a meta não inventa um total', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ enabled: false }), itemDoResumo({ id: 'b', name: 'Banco', state: 'unknown', enabled: false })]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { level: 2, name: 'Todos os alvos estão pausados.' })).toBeInTheDocument()
    expect(screen.getByText(/· nenhum alvo ativo$/)).toBeInTheDocument()
  })

  it('um ativo ainda sem dados: a manchete espera, e não diz "Tudo no ar."', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo(), itemDoResumo({ id: 'n', name: 'Novo', state: 'unknown', last_checked_at: null })]
    await r.cliente.refetchQueries()
    expect(await screen.findByRole('heading', { level: 2, name: 'Um serviço ainda sem dados.' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Tudo no ar.' })).not.toBeInTheDocument()
  })

  describe('tudo no ar', () => {
    // A rota ordena pela abertura: o último a terminar vem depois na lista.
    // Sabotagem: em ultimoResolvido, devolver o primeiro → aparece 08:30.
    it('mostra o check em círculo verde e a hora do último incidente resolvido, pelo fim', async () => {
      const r = await renderizarPainel('/')
      r.falsa.estado.resumo = [itemDoResumo()]
      r.falsa.estado.incidentes = [
        incidente({ id: 'i2', started_at: '2026-09-24T09:00:00Z', resolved_at: new Date(2026, 8, 24, 8, 30).toISOString() }),
        incidente({ id: 'i1', started_at: '2026-09-24T07:00:00Z', resolved_at: new Date(2026, 8, 24, 9, 12).toISOString() }),
      ]
      await r.cliente.refetchQueries()
      const titulo = await screen.findByRole('heading', { level: 2, name: 'Tudo no ar.' })
      expect(titulo.previousElementSibling).toHaveClass('bg-no-ar-100')
      expect(await screen.findByText(/^Último incidente resolvido em 24\/09\/2026,? 09:12\.$/)).toBeInTheDocument()
    })

    it('sem incidente resolvido, diz que não houve nenhum', async () => {
      const r = await renderizarPainel('/')
      r.falsa.estado.resumo = [itemDoResumo()]
      await r.cliente.refetchQueries()
      expect(await screen.findByText('Nenhum incidente registrado.')).toBeInTheDocument()
    })

    // Não saber não é "nenhum". Sabotagem: em fraseDoUltimoResolvido,
    // tratar undefined como lista vazia.
    it('se a consulta dos resolvidos falha, não diz "nenhum"', async () => {
      const r = await renderizarPainel('/')
      r.falsa.estado.resumo = [itemDoResumo()]
      r.falsa.estado.forcar.set('GET /incidents', { status: 502, code: 'unavailable' })
      await r.cliente.refetchQueries()
      await screen.findByRole('heading', { level: 2, name: 'Tudo no ar.' })
      await waitFor(() =>
        expect(r.cliente.getQueryCache().find({ queryKey: ['incidents', 'resolved'], exact: false })?.state.status).toBe('error'),
      )
      expect(screen.queryByText('Nenhum incidente registrado.')).not.toBeInTheDocument()
      expect(screen.queryByText(/^Último incidente resolvido/)).not.toBeInTheDocument()
    })
  })

  it('cartão de problema: a causa e há quanto tempo do incidente aberto, e o link para ele', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'b', name: 'Banco', state: 'down', last_latency_ms: null, open_incident_id: 'inc-9' }),
      itemDoResumo({ id: 'l', name: 'Lento', state: 'degraded', last_latency_ms: 2400 }),
    ]
    r.falsa.estado.incidentes = [incidente({ id: 'inc-9', target_id: 'b', target_name: 'Banco', cause: 'conn_refused' })]
    await r.cliente.refetchQueries()
    const banco = await cartaoDe('Banco')
    expect(banco).toHaveClass('border-l-fora')
    expect(await within(banco).findByText(/^Conexão recusada · há /)).toBeInTheDocument()
    expect(within(banco).getByRole('link', { name: 'Banco' })).toHaveAttribute('href', '/targets/b')
    expect(within(banco).getByRole('link', { name: 'Ver incidente de Banco' })).toHaveAttribute('href', '/incidents?status=open')

    const lento = await cartaoDe('Lento')
    expect(lento).toHaveClass('border-l-degradado')
    expect(within(lento).getByText('Latência de 2.400 ms no último check')).toBeInTheDocument()
    // "Ver detalhes" é pista visual: o cartão já leva ao detalhe.
    expect(within(lento).getAllByRole('link').map((a) => a.getAttribute('href'))).toEqual(['/targets/l'])
    expect(within(lento).getByText('Ver detalhes')).toHaveAttribute('aria-hidden', 'true')
  })

  // Review Focus 7.
  it('incidente sem causa: o cartão diz que a causa não foi registrada', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'b', name: 'Banco', state: 'down', open_incident_id: 'inc-9' })]
    r.falsa.estado.incidentes = [incidente({ id: 'inc-9', target_id: 'b', target_name: 'Banco', cause: '' })]
    await r.cliente.refetchQueries()
    expect(await within(await cartaoDe('Banco')).findByText(/^Causa não registrada · há /)).toBeInTheDocument()
  })

  // P12. Sabotagem: tirar o esqueleto={<EsqueletoDoPainel />} da tela → o
  // esqueleto padrão tem 3 blocos.
  it('enquanto o resumo não chega, o esqueleto do painel ocupa o lugar', async () => {
    const r = await renderizarPainel('/')
    let soltar!: () => void
    const segurado = new Promise<void>((ok) => (soltar = ok))
    servidor.use(
      http.get('*/api/v1/targets/summary', async () => {
        await segurado
        return HttpResponse.json({ items: [] })
      }),
    )
    void r.cliente.resetQueries({ queryKey: ['targets', 'summary'] })
    const ocupado = (await screen.findByText('Carregando…')).closest('[aria-busy="true"]') as HTMLElement
    expect(ocupado.querySelectorAll('[aria-hidden="true"]')).toHaveLength(8)
    soltar()
    expect(await screen.findByRole('heading', { level: 2, name: 'Nenhum serviço vigiado ainda.' })).toBeInTheDocument()
  })

  it('anuncia na região aria-live quando um alvo cai, e não na primeira carga', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'up' })]
    await r.cliente.refetchQueries()
    await screen.findByRole('link', { name: 'API' })
    const anuncios = screen.getByTestId('anuncios')
    expect(anuncios).toHaveTextContent('')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'down' })]
    await r.cliente.refetchQueries()
    await waitFor(() => expect(anuncios).toHaveTextContent('API: fora do ar'))
  })

  // Spec §4 e M9 da revisão: um poll de fundo que falha não esconde o que já
  // se sabia. Sabotagem prevista: em EstadoDaConsulta, trocar
  // "if (consulta.data === undefined)" por
  // "if (consulta.data === undefined || consulta.error)".
  it('um poll que falha mantém os alvos e avisa que os dados estão velhos', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API' })]
    await r.cliente.refetchQueries()
    await screen.findByRole('link', { name: 'API' })
    r.falsa.estado.forcar.set('GET /targets/summary', { status: 502, code: 'unavailable' })
    await r.cliente.refetchQueries({ queryKey: ['targets', 'summary'] })
    expect(await screen.findByText(/Não foi possível atualizar agora/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'API' })).toBeInTheDocument()
  })
})

describe('navegação', () => {
  // Spec §4.9: "Incidentes" com o contador de abertos; zero não aparece.
  it('mostra quantos incidentes estão abertos, e some quando não há', async () => {
    const r = await renderizarPainel('/')
    const link = await screen.findByRole('link', { name: 'Incidentes' })
    r.falsa.estado.incidentes = [
      incidente({ id: 'i1' }),
      incidente({ id: 'i2' }),
      incidente({ id: 'i3', resolved_at: '2026-09-24T11:30:00Z' }),
    ]
    await r.cliente.refetchQueries()
    await waitFor(() => expect(screen.getByRole('link', { name: 'Incidentes, 2 abertos' })).toBe(link))
    r.falsa.estado.incidentes = []
    await r.cliente.refetchQueries()
    await waitFor(() => expect(link).toHaveAccessibleName('Incidentes'))
  })

  // T5 da revisão de 2026-10-14: a fronteira do "99+" (uma página de até 100
  // abertos). 99 → "99"; 100 → "99+"; 101 (página cheia com next_cursor) →
  // "99+". Sabotagem: trocar "> 99" por "> 100" → o caso de 100 mostra "100".
  it.each([
    [99, 'Incidentes, 99 abertos'],
    [100, 'Incidentes, 99+ abertos'],
    [101, 'Incidentes, 99+ abertos'],
  ])('com %i incidentes abertos, o contador diz “%s”', async (quantos, nome) => {
    const r = await renderizarPainel('/')
    const link = await screen.findByRole('link', { name: 'Incidentes' })
    r.falsa.estado.incidentes = Array.from({ length: quantos }, (_, i) => incidente({ id: `i${i}` }))
    await r.cliente.refetchQueries()
    await waitFor(() => expect(link).toHaveAccessibleName(nome))
  })
})

// Plano 12: tirar os avisos de canais precisa derrubar estes casos. A API
// falsa só fornece respostas; a tela é real.
describe('avisos de canais', () => {
  it('sem canais avisa como configurar os alertas sem esconder os alvos', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo()]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Configurar canais' })).toHaveAttribute('href', '/channels')
    expect(screen.getByRole('link', { name: 'API' })).toBeInTheDocument()
  })

  it('avisa de canais pausados e de ausência de canal verificado ativo', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.canais = [canal({ enabled: false }), canal({ id: 'c2', status: 'pending' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('1 canal pausado. Os avisos desse canal são descartados.')).toBeInTheDocument()
    expect(screen.getByText('Nenhum canal verificado está ativo para receber avisos.')).toBeInTheDocument()
    r.falsa.estado.canais = [canal(), canal({ id: 'c2', enabled: false })]
    await r.cliente.refetchQueries({ queryKey: ['channels'] })
    await waitFor(() => expect(screen.queryByText('Nenhum canal verificado está ativo para receber avisos.')).not.toBeInTheDocument())
    expect(screen.getByText('1 canal pausado. Os avisos desse canal são descartados.')).toBeInTheDocument()
  })

  it('falha na consulta de canais não é apresentada como ausência e mantém os alvos', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo()]
    await r.cliente.refetchQueries()
    r.falsa.estado.forcar.set('GET /channels', { status: 502, code: 'unavailable' })
    await r.cliente.refetchQueries({ queryKey: ['channels'] })
    expect(await screen.findByText('Não foi possível conferir os canais de notificação agora.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'API' })).toBeInTheDocument()
  })

  // Um canal failing continua verificado e recebendo tentativas (notify).
  // Sabotagem: considerar apenas status verified no aviso de canal ativo.
  it('canal falhando é avisado e continua contado como verificado ativo', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.canais = [canal({ status: 'failing' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('1 canal está falhando na entrega.')).toBeInTheDocument()
    expect(screen.queryByText('Nenhum canal verificado está ativo para receber avisos.')).not.toBeInTheDocument()
  })
})
