import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { PING_URL } from '@/test/api-falsa'
import { heartbeat } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { ESPERA_DA_PREVIA_MS } from './PreviaDoCron'

const dialogo = () => screen.findByRole('dialog', { name: 'Guarde a URL de ping' })
const CANARIO = 'CANARIO-do-token-de-ping'

async function cadastrar(r: Awaited<ReturnType<typeof renderizarPainel>>, nome = 'Backup') {
  await r.usuario.type(await screen.findByLabelText('Nome'), nome)
  await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
}

async function detalheCom(p: Parameters<typeof heartbeat>[0] = {}, o: { navegador?: boolean } = {}) {
  const r = await renderizarPainel('/', o)
  r.falsa.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup', ...p })]
  await r.router.navigate('/heartbeats/h1')
  await screen.findByRole('heading', { level: 1, name: 'Backup' })
  return r
}

describe('cadastro', () => {
  it('o painel oferece cadastrar um heartbeat', async () => {
    await renderizarPainel('/')
    expect(await screen.findByRole('link', { name: 'Cadastrar heartbeat' })).toHaveAttribute('href', '/heartbeats/new')
  })

  // Sabotagem: mandar period_sec em minutos, ou não mostrar o diálogo.
  it('cadastra pelo período e mostra a URL com os exemplos por POST', async () => {
    const r = await renderizarPainel('/heartbeats/new')
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Backup')
    await r.usuario.clear(screen.getByLabelText('Período'))
    await r.usuario.type(screen.getByLabelText('Período'), '6')
    await r.usuario.selectOptions(screen.getByLabelText('Unidade de período'), 'horas')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    const d = await dialogo()
    expect(r.falsa.corpos('POST /heartbeats')).toEqual([
      { name: 'Backup', schedule: { kind: 'period', period_sec: 21600 }, grace_sec: 300 },
    ])
    expect(within(d).getByText(PING_URL)).toBeInTheDocument()
    expect(within(d).getByText(`curl -fsS -m 10 --retry 3 -o /dev/null -X POST ${PING_URL}`)).toBeInTheDocument()
    expect(within(d).getByText(`curl -fsS -m 10 --retry 3 -o /dev/null -X POST ${PING_URL}/start`)).toBeInTheDocument()
    expect(within(d).getByText(`curl -fsS -m 10 --retry 3 -o /dev/null -X POST ${PING_URL}/fail`)).toBeInTheDocument()
    expect(within(d).getByText(`curl -fsS -m 10 --retry 3 -o /dev/null -X POST ${PING_URL}/$?`)).toBeInTheDocument()
  })

  // Esc e o clique fora não fecham: a URL não volta. Sabotagem: aoMudar fechando, sem o `travado`.
  it('só "Já guardei" fecha, e leva ao detalhe', async () => {
    const r = await renderizarPainel('/heartbeats/new')
    await cadastrar(r)
    await dialogo()
    await r.usuario.keyboard('{Escape}')
    expect(screen.getByRole('dialog', { name: 'Guarde a URL de ping' })).toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Já guardei' }))
    await screen.findByRole('heading', { level: 1, name: 'Backup' })
    expect(r.router.state.location.pathname).toBe('/heartbeats/hb-1')
    expect(screen.queryByText(PING_URL)).not.toBeInTheDocument()
  })

  // Sabotagem: tirar o useSegurarNavegacao → o Voltar leva a URL embora.
  it('o Voltar não leva a URL embora, e "Já guardei" ainda navega', async () => {
    const r = await renderizarPainel('/')
    await r.router.navigate('/heartbeats/new')
    await cadastrar(r)
    await dialogo()
    await r.router.navigate(-1)
    await waitFor(() => expect(screen.getAllByText('Guarde a URL antes de sair: ela não aparece de novo.').length).toBeGreaterThan(0))
    expect(r.router.state.location.pathname).toBe('/heartbeats/new')
    expect(screen.getByRole('dialog', { name: 'Guarde a URL de ping' })).toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Já guardei' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/heartbeats/hb-1'))
  })

  it('o Voltar também espera a URL nova na rotação', async () => {
    const r = await detalheCom()
    await r.usuario.click(screen.getByRole('button', { name: 'Gerar nova URL' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Gerar nova URL' }))
    await dialogo()
    await r.router.navigate(-1)
    await waitFor(() => expect(screen.getAllByText('Guarde a URL antes de sair: ela não aparece de novo.').length).toBeGreaterThan(0))
    expect(r.router.state.location.pathname).toBe('/heartbeats/h1')
    await r.usuario.click(screen.getByRole('button', { name: 'Já guardei' }))
    await r.router.navigate(-1)
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
  })

  describe('prévia do cron', () => {
    async function comCron(r: Awaited<ReturnType<typeof renderizarPainel>>, expr: string) {
      await r.usuario.click(screen.getByRole('radio', { name: 'Expressão cron' }))
      const campo = screen.getByLabelText('Fuso')
      await r.usuario.clear(campo)
      await r.usuario.type(campo, 'America/Sao_Paulo')
      await r.usuario.type(screen.getByLabelText('Expressão'), expr)
    }

    // Às 6h UTC são 3h em São Paulo. Sabotagem: formatar sem timeZone → 06:00 na CI.
    it('mostra os próximos disparos no fuso declarado', async () => {
      const r = await renderizarPainel('/heartbeats/new')
      await comCron(r, '0 3 * * *')
      expect(await screen.findByText('Próximos disparos, em America/Sao_Paulo')).toBeInTheDocument()
      expect(screen.getByText('25/09/2026, 03:00')).toBeInTheDocument()
      expect(screen.getByText('26/09/2026, 03:00')).toBeInTheDocument()
    })

    // Sabotagem: sem a espera, uma consulta por tecla (9 aqui).
    it('espera a digitação parar: não é uma consulta por tecla', async () => {
      const r = await renderizarPainel('/heartbeats/new')
      await comCron(r, '0 3 * * *')
      await screen.findByText('Próximos disparos, em America/Sao_Paulo')
      expect(r.falsa.estado.previasPedidas.filter((q) => q.includes('expr=')).length).toBeLessThanOrEqual(2)
      expect(r.falsa.estado.previasPedidas.at(-1)).toBe('?expr=0+3+*+*+*&timezone=America%2FSao_Paulo')
    })

    // A prévia não trava: quem julga é o servidor, no envio (T11).
    it('expressão recusada mostra o motivo e não impede o envio', async () => {
      const r = await renderizarPainel('/heartbeats/new')
      r.falsa.estado.previaDoCron = null
      await r.usuario.type(screen.getByLabelText('Nome'), 'Backup')
      await comCron(r, 'lixo')
      expect(await screen.findByText('Use os cinco campos, com um disparo que exista: 0 3 * * *.')).toBeInTheDocument()
      expect(screen.queryByText('Próximos disparos, em America/Sao_Paulo')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Cadastrar' })).toBeEnabled()
    })

    it('sem expressão, nada de prévia nem de consulta', async () => {
      const r = await renderizarPainel('/heartbeats/new')
      await r.usuario.click(screen.getByRole('radio', { name: 'Expressão cron' }))
      await new Promise((resolver) => setTimeout(resolver, ESPERA_DA_PREVIA_MS + 50))
      expect(r.falsa.estado.previasPedidas).toEqual([])
    })
  })

  it('cadastra por cron, com o fuso', async () => {
    const r = await renderizarPainel('/heartbeats/new')
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Backup')
    await r.usuario.click(screen.getByRole('radio', { name: 'Expressão cron' }))
    await r.usuario.type(screen.getByLabelText('Expressão'), '0 3 * * *')
    await r.usuario.clear(screen.getByLabelText('Fuso'))
    await r.usuario.type(screen.getByLabelText('Fuso'), 'America/Sao_Paulo')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await dialogo()
    expect(r.falsa.corpos('POST /heartbeats')[0]?.schedule).toEqual({ kind: 'cron', expr: '0 3 * * *', timezone: 'America/Sao_Paulo' })
  })

  // Sabotagem: não mapear expr em campoDoServidor → o erro vai para o topo.
  it('o erro do servidor vai para o campo', async () => {
    const r = await renderizarPainel('/heartbeats/new')
    r.falsa.estado.forcar.set('POST /heartbeats', { status: 400, code: 'validation_failed', fields: [{ field: 'expr', reason: 'invalid' }] })
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Backup')
    await r.usuario.click(screen.getByRole('radio', { name: 'Expressão cron' }))
    await r.usuario.type(screen.getByLabelText('Expressão'), 'isso não é cron')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    expect(await screen.findByText('Use os cinco campos, com um disparo que exista: 0 3 * * *.')).toBeInTheDocument()
    expect(screen.getByLabelText('Expressão')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('o teto de 100 aparece como erro do formulário', async () => {
    const r = await renderizarPainel('/heartbeats/new')
    r.falsa.estado.forcar.set('POST /heartbeats', { status: 409, code: 'heartbeat_limit_reached' })
    await cadastrar(r)
    expect(await screen.findByRole('alert')).toHaveTextContent('Limite de 100 heartbeats atingido.')
  })

  it('campo inválido não chama o servidor', async () => {
    const r = await renderizarPainel('/heartbeats/new')
    await r.usuario.click(await screen.findByRole('button', { name: 'Cadastrar' }))
    expect(await screen.findByText('Informe um nome.')).toBeInTheDocument()
    expect(r.falsa.chamadas('POST /heartbeats')).toBe(0)
  })
})

describe('a URL de ping é segredo', () => {
  // Spec da tela, §Segredo, com o router sobre window.history: o de memória não
  // escreve lá. pushState e replaceState mostram toda entrada do histórico, e
  // não só a última. Sabotagens: setQueryData, { state: { url } }, a URL na
  // query, sessionStorage, console.info.
  it.each(['cadastro', 'rotação'])('a URL de ping não vai para o endereço, o histórico, o armazenamento, o cache nem o console (%s)', async (caminho) => {
    const espioes = (['log', 'info', 'warn', 'error', 'debug'] as const).map((m) => vi.spyOn(console, m))
    const entradas = [vi.spyOn(window.history, 'pushState'), vi.spyOn(window.history, 'replaceState')]
    const r = caminho === 'cadastro' ? await renderizarPainel('/heartbeats/new', { navegador: true }) : await detalheCom({}, { navegador: true })
    if (caminho === 'cadastro') {
      await cadastrar(r)
    } else {
      await r.usuario.click(screen.getByRole('button', { name: 'Gerar nova URL' }))
      await r.usuario.click(await screen.findByRole('button', { name: 'Gerar nova URL' }))
    }
    await dialogo()
    await r.usuario.click(screen.getByRole('button', { name: 'Já guardei' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())

    expect(entradas[0]?.mock.calls.length).toBeGreaterThan(0)
    const lugares = {
      endereco: window.location.href,
      historico: JSON.stringify(window.history.state),
      entradas: JSON.stringify(entradas.flatMap((s) => s.mock.calls)),
      local: JSON.stringify({ ...localStorage }),
      sessao: JSON.stringify({ ...sessionStorage }),
      cache: JSON.stringify(r.cliente.getQueryCache().getAll().map((q) => [q.queryKey, q.state.data])),
      mutacoes: JSON.stringify(r.cliente.getMutationCache().getAll().map((m) => m.state.data)),
      console: JSON.stringify(espioes.flatMap((s) => s.mock.calls)),
    }
    for (const [lugar, conteudo] of Object.entries(lugares)) expect(`${lugar}: ${conteudo}`).not.toContain(CANARIO)
  })
})

describe('edição', () => {
  it('sem mudança, nenhum PATCH', async () => {
    const r = await detalheCom()
    await r.usuario.click(screen.getByRole('link', { name: 'Editar' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Salvar' }))
    await screen.findByRole('heading', { level: 1, name: 'Backup' })
    expect(r.falsa.chamadas('PATCH /heartbeats/:id')).toBe(0)
  })

  // Sabotagem: mandar o formulário inteiro no PATCH.
  it('manda só o que mudou', async () => {
    const r = await detalheCom({ grace_sec: 900 })
    await r.usuario.click(screen.getByRole('link', { name: 'Editar' }))
    const nome = await screen.findByLabelText('Nome')
    expect(screen.getByLabelText('Tolerância')).toHaveValue(15)
    await r.usuario.type(nome, ' 2')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await screen.findByRole('heading', { level: 1, name: 'Backup 2' })
    expect(r.falsa.corpos('PATCH /heartbeats/:id')).toEqual([{ name: 'Backup 2' }])
  })
})

describe('ações do detalhe', () => {
  // Sabotagem: pausar direto, sem o Confirmar.
  it('pausar pede confirmação e retomar não', async () => {
    const r = await detalheCom()
    await r.usuario.click(screen.getByRole('button', { name: 'Pausar' }))
    expect(r.falsa.chamadas('PATCH /heartbeats/:id')).toBe(0)
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar heartbeat' }))
    await screen.findByRole('button', { name: 'Retomar' })
    expect(r.falsa.corpos('PATCH /heartbeats/:id')).toEqual([{ enabled: false }])
    expect(screen.getByText('Pausado')).toBeInTheDocument()

    await r.usuario.click(screen.getByRole('button', { name: 'Retomar' }))
    await screen.findByRole('button', { name: 'Pausar' })
    expect(r.falsa.corpos('PATCH /heartbeats/:id')).toEqual([{ enabled: false }, { enabled: true }])
  })

  it('apagar confirma, volta ao painel e tira o heartbeat da lista', async () => {
    const r = await detalheCom()
    await r.usuario.click(screen.getByRole('button', { name: 'Apagar' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar heartbeat' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    await screen.findByRole('heading', { level: 2, name: 'Nenhum serviço vigiado ainda.' })
    expect(r.falsa.estado.heartbeats).toEqual([])
  })

  // Sabotagem: rotacionar sem confirmar.
  it('gerar nova URL confirma e mostra a URL nova, uma vez', async () => {
    const r = await detalheCom()
    await r.usuario.click(screen.getByRole('button', { name: 'Gerar nova URL' }))
    expect(r.falsa.chamadas('POST /heartbeats/:id/token/rotate')).toBe(0)
    await r.usuario.click(await screen.findByRole('button', { name: 'Gerar nova URL' }))
    const d = await dialogo()
    expect(within(d).getByText(`${PING_URL}-novo`)).toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Já guardei' }))
    await waitFor(() => expect(screen.queryByText(`${PING_URL}-novo`)).not.toBeInTheDocument())
  })

  it('erro na ação aparece na tela e mantém o heartbeat', async () => {
    const r = await detalheCom()
    r.falsa.estado.forcar.set('POST /heartbeats/:id/token/rotate', { status: 500, code: 'internal_error' })
    await r.usuario.click(screen.getByRole('button', { name: 'Gerar nova URL' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Gerar nova URL' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Erro interno do servidor.')
    expect(screen.getByRole('heading', { level: 1, name: 'Backup' })).toBeInTheDocument()
  })

  // Sabotagem: tratar o 404 como erro comum → a tela fica.
  it('apagado em outra aba: a ação volta ao painel com aviso', async () => {
    const r = await detalheCom()
    r.falsa.estado.heartbeats = []
    await r.usuario.click(screen.getByRole('button', { name: 'Pausar' }))
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar heartbeat' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
    expect((await screen.findAllByText('O heartbeat já tinha sido apagado, talvez em outra aba.')).length).toBeGreaterThan(0)
  })

  it('apagado em outra aba: o poll também volta ao painel', async () => {
    const r = await detalheCom()
    r.falsa.estado.heartbeats = []
    await r.cliente.refetchQueries({ queryKey: ['heartbeats', 'h1'] })
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/'))
  })
})
