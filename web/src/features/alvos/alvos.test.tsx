import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { Alvo } from '@/lib/api/schemas'
import { esquemaAlvo, paraAlteracao, paraCriacao, valoresDe, valoresPadrao } from './esquema'

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
