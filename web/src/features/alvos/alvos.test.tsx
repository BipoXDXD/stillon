import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { Alvo } from '@/lib/api/schemas'
import {
  apagaOsHeaders,
  esquemaAlvo,
  esquemaDoAlvo,
  paraAlteracao,
  paraCriacao,
  valoresDe,
  valoresPadrao,
} from './esquema'

function erroDe(v: Partial<typeof valoresPadrao>, campo: string) {
  const r = esquemaAlvo.safeParse({ ...valoresPadrao, name: 'API', url: 'https://exemplo.com', ...v })
  return r.success ? undefined : r.error.issues.find((i) => i.path.join('.') === campo)?.message
}

describe('o espelho de validate.go', () => {
  it('timeout tem de ser menor que 90% do intervalo (D3 do Plano 2)', () => {
    expect(erroDe({ interval_sec: 30, timeout_ms: 27000 }, 'timeout_ms')).toBe('Tem de ser menor que 90% do intervalo.')
    expect(erroDe({ interval_sec: 30, timeout_ms: 26999 }, 'timeout_ms')).toBeUndefined()
  })

  it('endereço interno óbvio é recusado já no cliente; um público passa', () => {
    for (const u of ['http://localhost/x', 'http://127.0.0.1', 'http://10.1.2.3', 'http://[::1]/', 'http://169.254.169.254/']) {
      expect(erroDe({ url: u }, 'url')).toMatch(/internos/)
    }
    expect(erroDe({ url: 'ftp://exemplo.com' }, 'url')).toBe('Só http:// e https://.')
    // O cliente nunca pode ser mais restritivo que o servidor: IPv4 mapeado
    // público passa pelo safedial (Unmap), então passa aqui também.
    expect(erroDe({ url: 'http://[::ffff:8.8.8.8]/' }, 'url')).toBeUndefined()
    expect(erroDe({ url: 'https://vigilume-e2e.invalid/' }, 'url')).toBeUndefined()
  })

  it('headers: proibidos, repetidos e limites', () => {
    const h = (nome: string, valor = 'x') => ({ nome, valor })
    expect(erroDe({ headers: [h('Host')] }, 'headers.0.nome')).toBe('Este header é controlado pelo monitor.')
    expect(erroDe({ headers: [h('X-A'), h('x-a')] }, 'headers.1.nome')).toBe('Header repetido.')
    expect(erroDe({ headers: [h('com espaço')] }, 'headers.0.nome')).toBe('Nome de header inválido.')
    expect(erroDe({ headers: [h('X-A', 'v'.repeat(1025))] }, 'headers.0.valor')).toBe('Até 1 KiB.')
  })

  it('nome com 100 code points passa; com 101, não', () => {
    expect(erroDe({ name: 'é'.repeat(100) }, 'name')).toBeUndefined()
    expect(erroDe({ name: 'é'.repeat(101) }, 'name')).toBe('Até 100 caracteres.')
  })
})

describe('os corpos enviados', () => {
  it('criação: limiar padrão vai ausente, desativado vai null', () => {
    const v = { ...valoresPadrao, name: 'API', url: 'https://exemplo.com' }
    expect(paraCriacao(v)).not.toHaveProperty('degraded_threshold_ms')
    expect(paraCriacao({ ...v, lentidao: 'desativado' }).degraded_threshold_ms).toBeNull()
    expect(paraCriacao({ ...v, lentidao: 'personalizado', degraded_threshold_ms: 800 }).degraded_threshold_ms).toBe(800)
  })

  // D24: sem "substituir" ou "remover", o PATCH NÃO leva a chave headers.
  // Sabotagem prevista: sempre pôr "a.headers = mapaDeHeaders(v)" → {} vai
  // no corpo e apagaria os headers em silêncio.
  it('edição só manda o que mudou, e nunca "headers" sem pedir', () => {
    const original = valoresDe(Alvo.parse(alvo({ header_names: ['Authorization'] })))
    expect(paraAlteracao(original, { ...original, name: 'Outro' })).toEqual({ name: 'Outro' })
    expect(paraAlteracao(original, original)).toEqual({})
    expect(paraAlteracao(original, { ...original, modoHeaders: 'remover' })).toEqual({ headers: null })
    expect(
      paraAlteracao(original, { ...original, modoHeaders: 'substituir', headers: [{ nome: 'X-A', valor: '1' }] }),
    ).toEqual({ headers: { 'X-A': '1' } })
  })
})

// B1 do Plano 10: um PATCH que troca a origem da URL (esquema, host ou
// porta) sem mandar headers faz o servidor apagá-los. A comparação é a do
// new URL(...).origin, com as portas padrão normalizadas.
describe('troca de destino com headers', () => {
  const url = 'https://api.exemplo.com/health'
  it('só avisa com headers definidos, a origem mudada e sem headers no PATCH', () => {
    const nomes = ['Authorization']
    expect(apagaOsHeaders(nomes, url, { url: 'https://outro.exemplo.com/health' })).toBe(true)
    expect(apagaOsHeaders(nomes, url, { url: 'http://api.exemplo.com/health' })).toBe(true)
    expect(apagaOsHeaders(nomes, url, { url: 'https://api.exemplo.com:8443/health' })).toBe(true)
    // Só o caminho, ou a porta padrão escrita por extenso: a mesma origem.
    expect(apagaOsHeaders(nomes, url, { url: 'https://api.exemplo.com/outro?x=1' })).toBe(false)
    expect(apagaOsHeaders(nomes, url, { url: 'https://API.exemplo.com:443/health' })).toBe(false)
    // Os espaços das pontas saem antes, como no servidor (strings.TrimSpace,
    // que também tira o espaço sem quebra que o parser de URL não tira).
    expect(apagaOsHeaders(nomes, url, { url: '\u00a0https://api.exemplo.com/outro ' })).toBe(false)
    // Uma URL que não se lê conta como outra origem: avisar a mais.
    expect(apagaOsHeaders(nomes, url, { url: 'nada disso' })).toBe(true)
    // Sem headers guardados, sem URL nova, ou com headers no PATCH
    // (substituir ou remover): nada a avisar.
    expect(apagaOsHeaders([], url, { url: 'https://outro.exemplo.com/' })).toBe(false)
    expect(apagaOsHeaders(nomes, url, { name: 'Outro' })).toBe(false)
    expect(apagaOsHeaders(nomes, url, { url: 'https://outro.exemplo.com/', headers: { 'X-A': '1' } })).toBe(false)
    expect(apagaOsHeaders(nomes, url, { url: 'https://outro.exemplo.com/', headers: null })).toBe(false)
  })
})

describe('cadastrar', () => {
  async function preencher(usuario: Awaited<ReturnType<typeof renderizarPainel>>['usuario']) {
    await usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
  }

  it('cria e leva ao detalhe', async () => {
    const r = await renderizarPainel('/targets/new')
    await preencher(r.usuario)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/targets/alvo-1'))
    expect(r.falsa.corpos('POST /targets')[0]).toMatchObject({
      name: 'Site',
      url: 'https://exemplo.com',
      method: 'GET',
      interval_sec: 60,
      timeout_ms: 10000,
      expected_status: 200,
      enabled: true,
    })
  })

  it('o 400 com fields vai para o campo certo', async () => {
    const r = await renderizarPainel('/targets/new')
    r.falsa.estado.forcar.set('POST /targets', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'url', reason: 'forbidden' }],
    })
    await preencher(r.usuario)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    const url = screen.getByLabelText('URL')
    await waitFor(() => expect(url).toHaveAttribute('aria-invalid', 'true'))
    expect(url).toHaveAccessibleDescription(/Endereços internos/)
  })

  it('o teto de 100 alvos (Plano 5, D24) aparece como erro do formulário', async () => {
    const r = await renderizarPainel('/targets/new')
    r.falsa.estado.forcar.set('POST /targets', { status: 409, code: 'target_limit_reached' })
    await preencher(r.usuario)
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Limite de 100 alvos atingido.')
  })
})

describe('editar', () => {
  it('mostra os headers definidos sem valores, e salvar sem mexer não manda headers', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo({ header_names: ['Authorization', 'X-Chave'] })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Headers definidos: Authorization, X-Chave (valores ocultos).')).toBeInTheDocument()
    const nome = screen.getByLabelText('Nome')
    await r.usuario.clear(nome)
    await r.usuario.type(nome, 'API nova')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toHaveLength(1))
    expect(r.falsa.corpos('PATCH /targets/:id')[0]).toEqual({ name: 'API nova' })
  })

  // B1 do Plano 10: trocar o host de um alvo com headers pede confirmação,
  // porque o servidor os apaga. Cancelar não manda nada, e o foco não cai
  // no <body>; confirmar manda o PATCH só com a URL, e os headers somem.
  // Sabotagem: não perguntar (tirar o "await perguntar()" da edição) → o
  // PATCH sai sem o alertdialog.
  it('trocar o destino de um alvo com headers avisa que eles serão apagados', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo({ header_names: ['Authorization'] })]
    await r.cliente.refetchQueries()
    await screen.findByText('Headers definidos: Authorization (valores ocultos).')
    const url = screen.getByLabelText('URL')
    await r.usuario.clear(url)
    await r.usuario.type(url, 'https://outro.exemplo.com/health')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const aviso = await screen.findByRole('alertdialog', { name: 'Trocar o destino do alvo?' })
    expect(aviso).toHaveTextContent('Os headers deste alvo serão apagados, porque o destino mudou (o esquema, o host ou a porta da URL).')
    expect(r.falsa.chamadas('PATCH /targets/:id')).toBe(0)
    await r.usuario.click(within(aviso).getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(r.falsa.chamadas('PATCH /targets/:id')).toBe(0)
    expect(document.activeElement).not.toBe(document.body)
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const deNovo = await screen.findByRole('alertdialog', { name: 'Trocar o destino do alvo?' })
    await r.usuario.click(within(deNovo).getByRole('button', { name: 'Salvar e apagar os headers' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toHaveLength(1))
    expect(r.falsa.corpos('PATCH /targets/:id')[0]).toEqual({ url: 'https://outro.exemplo.com/health' })
    expect(r.falsa.estado.alvos[0]!.header_names).toEqual([])
  })

  // Só o caminho muda: a origem é a mesma, e os headers ficam. Sem aviso.
  it('trocar só o caminho de um alvo com headers salva sem aviso', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo({ header_names: ['Authorization'] })]
    await r.cliente.refetchQueries()
    await screen.findByText('Headers definidos: Authorization (valores ocultos).')
    const url = screen.getByLabelText('URL')
    await r.usuario.clear(url)
    await r.usuario.type(url, 'https://api.exemplo.com/status')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toHaveLength(1))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(r.falsa.estado.alvos[0]!.header_names).toEqual(['Authorization'])
  })

  // Spec §4.3: intervalo fora da lista ("Outro…", 30 a 86400 s). Um alvo com
  // 120 s abre com "Outro…" e o campo em segundos, e não com o primeiro item
  // da lista (que, salvo sem mexer, mudaria o intervalo em silêncio).
  // Sabotagem prevista: em escolhaDoIntervalo, trocar ": OUTRO" por ": '60'".
  it('intervalo fora da lista abre em "Outro…", com o valor, e só ele vai no PATCH', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo({ interval_sec: 120 })]
    await r.cliente.refetchQueries()
    const segundos = await screen.findByLabelText('Intervalo em segundos')
    expect(screen.getByLabelText('Intervalo')).toHaveDisplayValue('Outro…')
    expect(segundos).toHaveValue(120)
    await r.usuario.clear(segundos)
    await r.usuario.type(segundos, '45')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toHaveLength(1))
    expect(r.falsa.corpos('PATCH /targets/:id')[0]).toEqual({ interval_sec: 45 })
  })
})

// Revisão final: voltar pelo histórico de /targets/B/edit a /targets/A/edit
// (A no cache) reaproveitava o formulário de B: a tela mostrava B e o PATCH
// ia para A com a diferença contra B. Sabotagem: tirar o key={alvo.id} de
// <EdicaoDoAlvo> → o Nome continua "Site B".
describe('editar dois alvos seguidos', () => {
  it('voltar pelo histórico a outro alvo mostra e salva o alvo da URL', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [
      alvo({ id: 'alvo-1', name: 'Site A', url: 'https://a.exemplo.com/' }),
      alvo({ id: 'alvo-2', name: 'Site B', url: 'https://b.exemplo.com/', interval_sec: 300 }),
    ]
    await r.cliente.refetchQueries()
    await waitFor(() => expect(screen.getByLabelText('Nome')).toHaveValue('Site A'))
    await r.router.navigate('/targets/alvo-2/edit')
    await waitFor(() => expect(screen.getByLabelText('Nome')).toHaveValue('Site B'))
    await r.router.navigate(-1)
    await waitFor(() => expect(r.router.state.location.pathname).toBe('/targets/alvo-1/edit'))
    await waitFor(() => expect(screen.getByLabelText('Nome')).toHaveValue('Site A'))
    expect(screen.getByLabelText('URL')).toHaveValue('https://a.exemplo.com/')
    await r.usuario.selectOptions(screen.getByLabelText('Intervalo'), '5 minutos')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toEqual([{ interval_sec: 300 }]))
    expect(r.falsa.estado.alvos.map((a) => [a.id, a.name, a.interval_sec])).toEqual([
      ['alvo-1', 'Site A', 300],
      ['alvo-2', 'Site B', 300],
    ])
  })
})

describe('intervalo "Outro…"', () => {
  it('fora de 30 a 86400 é recusado no campo; um da lista esconde o campo', async () => {
    const r = await renderizarPainel('/targets/new')
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    await r.usuario.selectOptions(screen.getByLabelText('Intervalo'), 'Outro…')
    const segundos = screen.getByLabelText('Intervalo em segundos')
    await r.usuario.clear(segundos)
    await r.usuario.type(segundos, '29')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(segundos).toHaveAccessibleDescription(/Mínimo de 30 s/))
    expect(r.falsa.chamadas('POST /targets')).toBe(0)
    await r.usuario.selectOptions(screen.getByLabelText('Intervalo'), '5 minutos')
    expect(screen.queryByLabelText('Intervalo em segundos')).not.toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.falsa.corpos('POST /targets')[0]).toMatchObject({ interval_sec: 300 }))
  })
})

describe('ajustes da revisão (rodada 1)', () => {
  // Sabotagem: devolver ao esquema a faixa incondicional do limiar.
  it('limiar inválido escondido não conta; só vale no personalizado', () => {
    for (const n of [0, 70000, NaN]) {
      expect(erroDe({ lentidao: 'padrao', degraded_threshold_ms: n }, 'degraded_threshold_ms')).toBeUndefined()
      expect(erroDe({ lentidao: 'desativado', degraded_threshold_ms: n }, 'degraded_threshold_ms')).toBeUndefined()
    }
    expect(erroDe({ lentidao: 'personalizado', degraded_threshold_ms: 0 }, 'degraded_threshold_ms')).toBe(
      'Entre 1 e 60000 ms.',
    )
    expect(erroDe({ lentidao: 'personalizado', degraded_threshold_ms: NaN }, 'degraded_threshold_ms')).toBe(
      'Informe um número.',
    )
  })

  it('limiar 0 no personalizado, depois Padrão: o POST sai, sem a chave', async () => {
    const r = await renderizarPainel('/targets/new')
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    await r.usuario.click(screen.getByLabelText('Personalizado'))
    const limiar = screen.getByLabelText('Limiar (ms)')
    await r.usuario.clear(limiar)
    await r.usuario.type(limiar, '0')
    await r.usuario.click(screen.getByLabelText('Padrão (2000 ms)'))
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.falsa.chamadas('POST /targets')).toBe(1))
    expect(r.falsa.corpos('POST /targets')[0]).not.toHaveProperty('degraded_threshold_ms')
  })

  // Sabotagem: voltar a "const iniciais = valoresDe(alvo)" a cada render.
  it('a base do PATCH não anda com a nova busca: só o nome vai', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo()]
    await r.cliente.refetchQueries()
    const nome = await screen.findByLabelText('Nome')
    await waitFor(() => expect(nome).toHaveValue('API'))
    // Outra pessoa muda a URL; a consulta volta a buscar (foco, 60 s).
    r.falsa.estado.alvos = [alvo({ url: 'https://mudou.exemplo.com/' })]
    await r.cliente.refetchQueries()
    await r.usuario.clear(nome)
    await r.usuario.type(nome, 'API nova')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toHaveLength(1))
    expect(r.falsa.corpos('PATCH /targets/:id')[0]).toEqual({ name: 'API nova' })
  })

  it('IPv6 só pelos hextetos inteiros: [fc::1] e [fe8::1] são públicos para o safedial', () => {
    expect(erroDe({ url: 'http://[fc::1]/' }, 'url')).toBeUndefined()
    expect(erroDe({ url: 'http://[fe8::1]/' }, 'url')).toBeUndefined()
    expect(erroDe({ url: 'http://[fc00::1]/' }, 'url')).toMatch(/internos/)
    expect(erroDe({ url: 'http://[fe80::1]/' }, 'url')).toMatch(/internos/)
  })

  it('na edição, a URL já guardada não passa de novo pelo espelho; uma nova, sim', () => {
    const base = { url: 'http://0x7f.1/' }
    const v = { ...valoresPadrao, name: 'API', modoHeaders: 'manter' as const }
    expect(esquemaDoAlvo(base).safeParse({ ...v, url: 'http://0x7f.1/' }).success).toBe(true)
    expect(esquemaDoAlvo(base).safeParse({ ...v, url: 'http://127.0.0.1/' }).success).toBe(false)
    expect(esquemaAlvo.safeParse({ ...v, url: 'http://0x7f.1/' }).success).toBe(false)
  })

  it('timeout contra intervalo só com o intervalo na faixa, como validate.go', () => {
    expect(erroDe({ interval_sec: 20, timeout_ms: 27000 }, 'timeout_ms')).toBeUndefined()
    expect(erroDe({ interval_sec: 20, timeout_ms: 27000 }, 'interval_sec')).toBe('Mínimo de 30 s.')
  })

  it('erro de interval_sec do servidor com um intervalo da lista: o seletor recebe o erro e o foco', async () => {
    const r = await renderizarPainel('/targets/new')
    r.falsa.estado.forcar.set('POST /targets', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'interval_sec', reason: 'out_of_range' }],
    })
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    const seletor = screen.getByLabelText('Intervalo')
    await waitFor(() => expect(seletor).toHaveAttribute('aria-invalid', 'true'))
    expect(seletor).toHaveFocus()
  })

  it('"Substituir todos" sem nenhuma linha não salva, e aponta "Remover todos"', async () => {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo({ header_names: ['Authorization'] })]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByLabelText('Substituir todos'))
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await screen.findByText('Adicione ao menos um header, ou escolha “Remover todos”.')).toBeInTheDocument()
    expect(r.falsa.chamadas('PATCH /targets/:id')).toBe(0)
    await r.usuario.click(screen.getByLabelText('Remover todos'))
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos('PATCH /targets/:id')).toEqual([{ headers: null }]))
  })
})
