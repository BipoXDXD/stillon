import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { canal, incidente, itemDoResumo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { ItemDoResumo } from '@/lib/api/schemas'
import { mapaDeEstados, transicoes } from './transicoes'

const resumido = (p: Parameters<typeof itemDoResumo>[0]) => ItemDoResumo.parse(itemDoResumo(p))

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
  it('sem alvos, oferece cadastrar o primeiro', async () => {
    await renderizarPainel('/')
    expect(await screen.findByRole('link', { name: 'Cadastrar o primeiro alvo' })).toHaveAttribute('href', '/targets/new')
  })

  it('mostra os cinco estados com texto, e pausado no lugar de "sem dados" para alvo desativado', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'a', name: 'No ar', state: 'up', uptime_24h: 0.99999 }),
      itemDoResumo({ id: 'b', name: 'Lento', state: 'degraded' }),
      itemDoResumo({ id: 'c', name: 'Caiu', state: 'down', last_latency_ms: null, open_incident_id: 'inc-1' }),
      itemDoResumo({ id: 'd', name: 'Novo', state: 'unknown', last_checked_at: null, uptime_24h: null, spark: [] }),
      itemDoResumo({ id: 'e', name: 'Parado', state: 'unknown', enabled: false }),
    ]
    await r.cliente.refetchQueries()
    const cartao = (nome: string) => screen.getByRole('link', { name: nome }).closest('li') as HTMLElement
    await screen.findByRole('link', { name: 'No ar' })
    expect(within(cartao('No ar')).getByText('No ar', { selector: 'span' })).toBeInTheDocument()
    // Truncado: 0,99999 é "99,99%", nunca "100,00%".
    expect(within(cartao('No ar')).getByText('99,99%')).toBeInTheDocument()
    expect(within(cartao('Lento')).getByText('Degradado')).toBeInTheDocument()
    expect(within(cartao('Caiu')).getByText('Fora do ar')).toBeInTheDocument()
    expect(within(cartao('Caiu')).getByText('—')).toBeInTheDocument()
    expect(within(cartao('Caiu')).getByRole('link', { name: 'Incidente aberto' })).toBeInTheDocument()
    expect(within(cartao('Novo')).getByText('Sem dados')).toBeInTheDocument()
    expect(within(cartao('Novo')).getByText('nunca')).toBeInTheDocument()
    expect(within(cartao('Parado')).getByText('Pausado')).toBeInTheDocument()
    expect(screen.getByText('1 no ar, 1 degradado, 1 fora do ar, 1 sem dados, 1 pausado')).toBeInTheDocument()
    // O ícone é decorativo: o leitor de tela ouve só o texto.
    for (const svg of within(cartao('Caiu')).getAllByText('Fora do ar')[0]!.querySelectorAll('svg')) {
      expect(svg).toHaveAttribute('aria-hidden', 'true')
    }
  })

  it('anuncia na região aria-live quando um alvo cai, e não na primeira carga', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'up' })]
    await r.cliente.refetchQueries()
    await screen.findByRole('link', { name: 'API' })
    const regiao = screen.getByTestId('anuncios')
    expect(regiao).toHaveTextContent('')
    r.falsa.estado.resumo = [itemDoResumo({ id: 'a', name: 'API', state: 'down' })]
    await r.cliente.refetchQueries()
    await waitFor(() => expect(regiao).toHaveTextContent('API: fora do ar'))
  })

  // Spec §4 e M9 da revisão: um poll de fundo que falha não esconde o que já
  // se sabia. Sabotagem prevista: em EstadoDaConsulta, trocar
  // "if (consulta.data === undefined)" por
  // "if (consulta.data === undefined || consulta.error)".
  it('um poll que falha mantém os cartões e avisa que os dados estão velhos', async () => {
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
})

// Plano 12: tirar a contagem por enabled ou os avisos de canais precisa
// derrubar estes casos. A API falsa só fornece respostas; a tela é real.
describe('resumo operacional', () => {
  it('conta os estados reais e separa alvo pausado de sem dados', async () => {
    const r = await renderizarPainel('/')
    r.falsa.estado.resumo = [
      itemDoResumo({ id: 'a1' }), itemDoResumo({ id: 'a2' }),
      itemDoResumo({ id: 'a3', state: 'degraded' }),
      itemDoResumo({ id: 'a4', state: 'down' }),
      itemDoResumo({ id: 'a5', state: 'unknown' }),
      itemDoResumo({ id: 'a6', state: 'up', enabled: false }),
    ]
    await r.cliente.refetchQueries()
    const resumo = await screen.findByRole('region', { name: 'Resumo dos alvos' })
    for (const [estado, numero] of [['No ar', '2'], ['Degradado', '1'], ['Fora do ar', '1'], ['Sem dados', '1'], ['Pausado', '1']] as const) {
      expect(within(resumo).getByText(estado).nextElementSibling).toHaveTextContent(numero)
    }
    expect(screen.getByText(/Última atualização:/)).toBeInTheDocument()
  })

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
