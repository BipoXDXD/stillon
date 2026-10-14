import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { alvo, itemDoResumo, pagina } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'
import { sugerirSlug } from './PaginaStatusPage'

const PATCH = 'PATCH /status-pages/:id'
const POST = 'POST /status-pages'

function respostaControlada() {
  let soltar!: () => void
  const liberada = new Promise<void>((resolve) => { soltar = resolve })
  return { liberada, soltar }
}

async function montar(p: {
  pagina?: ReturnType<typeof pagina>
  alvos?: ReturnType<typeof alvo>[]
  resumo?: ReturnType<typeof itemDoResumo>[]
} = {}) {
  const r = await renderizarPainel('/status-pages')
  r.falsa.estado.alvos = p.alvos ?? [alvo()]
  r.falsa.estado.paginas = p.pagina ? [p.pagina] : []
  r.falsa.estado.resumo = p.resumo ?? []
  await r.cliente.refetchQueries()
  await screen.findByLabelText('Título')
  return r
}

function comItem() {
  return pagina({ items: [{ target_id: 'alvo-1', display_name: 'API pública' }] })
}

describe('status page: contratos públicos complementares', () => {
  it('a sugestão trunca sem deixar hífen final e não transforma título sem letras em endereço', () => {
    expect(sugerirSlug('a'.repeat(39) + ' b')).toBe('a'.repeat(39))
    expect(sugerirSlug('Áção & São Paulo!')).toBe('acao-sao-paulo')
    expect(sugerirSlug('!!!')).toBe('')
  })

  it('blur não inventa slug vazio nem sobrescreve um endereço escolhido pela pessoa', async () => {
    const r = await montar()
    await r.usuario.type(screen.getByLabelText('Título'), '!!!')
    await r.usuario.tab()
    expect(screen.getByLabelText('Endereço (slug)')).toHaveValue('')
    expect(screen.getByLabelText('Endereço (slug)')).not.toHaveAttribute('aria-invalid')
    await r.usuario.type(screen.getByLabelText('Endereço (slug)'), 'endereco-escolhido')
    fireEvent.change(screen.getByLabelText('Título'), { target: { value: 'Outro título' } })
    await r.usuario.click(screen.getByLabelText('Título'))
    await r.usuario.tab()
    expect(screen.getByLabelText('Endereço (slug)')).toHaveValue('endereco-escolhido')
  })

  it('o envio normaliza título e nome de exibição, preserva publicação e impede título vazio', async () => {
    const r = await montar()
    fireEvent.change(screen.getByLabelText('Título'), { target: { value: '   ' } })
    await r.usuario.type(screen.getByLabelText('Endereço (slug)'), 'empresa')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(screen.getByLabelText('Título')).toHaveAccessibleDescription('Obrigatório.'))
    expect(r.falsa.chamadas(POST)).toBe(0)
    fireEvent.change(screen.getByLabelText('Título'), { target: { value: '  Minha empresa  ' } })
    await r.usuario.selectOptions(screen.getByLabelText('Acrescentar alvo'), 'alvo-1')
    await r.usuario.click(screen.getByRole('button', { name: 'Acrescentar' }))
    fireEvent.change(screen.getByLabelText('Nome de exibição de API'), { target: { value: '  Serviço público  ' } })
    await r.usuario.click(screen.getByRole('checkbox', { name: /Publicada/ }))
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await screen.findByRole('button', { name: 'Salvar' })
    expect(r.falsa.corpos(POST)[0]).toEqual({
      title: 'Minha empresa', slug: 'empresa', published: true,
      items: [{ target_id: 'alvo-1', display_name: 'Serviço público' }],
    })
    expect(screen.getByRole('link', { name: 'Abrir' })).toHaveAttribute('href', 'http://localhost:5173/p/empresa')
  })

  it.each([
    ['Título', '🙂'.repeat(101)], ['Nome de exibição de API', '🙂'.repeat(101)],
  ])('mais de 100 pontos de código em %s bloqueia o PATCH', async (rotulo, texto) => {
    const r = await montar({ pagina: comItem() })
    fireEvent.change(screen.getByLabelText(rotulo), { target: { value: texto } })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(screen.getByLabelText(rotulo)).toHaveAccessibleDescription('Até 100 caracteres.'))
    expect(r.falsa.chamadas(PATCH)).toBe(0)
  })

  it.each([
    ['Título', 'inversão bidi', 'Status‮oficial'],
    ['Título', 'espaço de largura zero', 'Status​oficial'],
    ['Nome de exibição de API', 'hífen suave', 'API­pública'],
    ['Nome de exibição de API', 'ZWJ no fim', 'API pública‍'],
  ])('%s com %s mostra o motivo no campo e bloqueia o PATCH', async (rotulo, _caso, texto) => {
    const r = await montar({ pagina: comItem() })
    fireEvent.change(screen.getByLabelText(rotulo), { target: { value: texto } })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() =>
      expect(screen.getByLabelText(rotulo)).toHaveAccessibleDescription(
        'Sem caracteres invisíveis ou de controle (direção do texto, largura zero, hífen suave).',
      ),
    )
    expect(r.falsa.chamadas(PATCH)).toBe(0)
  })

  it('emoji com ZWJ e ZWNJ persa entre letras são enviados', async () => {
    const r = await montar({ pagina: comItem() })
    fireEvent.change(screen.getByLabelText('Título'), { target: { value: 'Equipe 👩‍💻' } })
    fireEvent.change(screen.getByLabelText('Nome de exibição de API'), { target: { value: 'می‌خواهم' } })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.chamadas(PATCH)).toBe(1))
    expect(r.falsa.corpos(PATCH)[0]).toMatchObject({
      title: 'Equipe 👩‍💻',
      items: [{ target_id: 'alvo-1', display_name: 'می‌خواهم' }],
    })
  })

  it('100 pontos de código multibyte em título e item são enviados sem contar unidades UTF-16', async () => {
    const r = await montar({ pagina: comItem() })
    const texto = '🙂'.repeat(100)
    fireEvent.change(screen.getByLabelText('Título'), { target: { value: texto } })
    fireEvent.change(screen.getByLabelText('Nome de exibição de API'), { target: { value: texto } })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.chamadas(PATCH)).toBe(1))
    expect(r.falsa.corpos(PATCH)[0]).toMatchObject({ title: texto, items: [{ target_id: 'alvo-1', display_name: texto }] })
  })

  it.each(['ab', '-abc', 'abc-', 'Abc', 'a_b', 'a'.repeat(41)])('slug inválido %s não chega ao servidor', async (slug) => {
    const r = await montar()
    await r.usuario.type(screen.getByLabelText('Título'), 'Empresa')
    fireEvent.change(screen.getByLabelText('Endereço (slug)'), { target: { value: slug } })
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(screen.getByLabelText('Endereço (slug)')).toHaveAttribute('aria-invalid', 'true'))
    expect(r.falsa.chamadas(POST)).toBe(0)
  })

  it.each([
    ['title', 'required', 'Título', 'Obrigatório.'],
    ['slug', 'invalid', 'Endereço (slug)', 'De 3 a 40 caracteres: letras minúsculas, números e hífens, sem hífen nas pontas.'],
    ['items[0].display_name', 'too_long', 'Nome de exibição de API', 'Longo demais.'],
  ])('erro HTTP %s vai ao controle correto', async (field, reason, rotulo, texto) => {
    const r = await montar({ pagina: comItem() })
    r.falsa.estado.forcar.set(PATCH, { status: 400, code: 'validation_failed', fields: [{ field, reason }] })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(screen.getByLabelText(rotulo)).toHaveAccessibleDescription(new RegExp(texto)))
    expect(screen.getByLabelText(rotulo)).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it.each(['items', 'items[0].target_id'])('erro HTTP %s aparece na lista, sem caminho cru ou erro no nome', async (field) => {
    const r = await montar({ pagina: comItem() })
    r.falsa.estado.forcar.set(PATCH, { status: 400, code: 'validation_failed', fields: [{ field, reason: 'invalid' }] })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const grupo = screen.getByRole('group', { name: 'Alvos na página (1 de 20)' })
    expect(await within(grupo).findByText('Valor inválido.')).toBeInTheDocument()
    expect(screen.getByLabelText('Nome de exibição de API')).not.toHaveAttribute('aria-invalid')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByText(field)).not.toBeInTheDocument()
  })

  it('campo HTTP desconhecido recebe aviso geral; resposta bem-sucedida seguinte limpa o erro', async () => {
    const r = await montar({ pagina: comItem() })
    // A forma do envelope admite fields novos; published não é erro de
    // validação emitido pelo serviço Go atual. Este caso é defensivo HTTP.
    r.falsa.estado.forcar.set(PATCH, { status: 400, code: 'validation_failed', fields: [{ field: 'published', reason: 'invalid' }] })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Valor inválido.')
    r.falsa.estado.forcar.delete(PATCH)
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const avisos = within(screen.getByRole('region', { name: 'Notifications (F8)' }))
    expect(await avisos.findByText('Status page salva. A página pública muda em até 30 segundos.')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('erro sem fields fica no formulário e não apaga o texto editado', async () => {
    const r = await montar({ pagina: comItem() })
    await r.usuario.clear(screen.getByLabelText('Título'))
    await r.usuario.type(screen.getByLabelText('Título'), 'Título editado')
    r.falsa.estado.forcar.set(PATCH, { status: 503, code: 'unavailable' })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Serviço indisponível no momento.')
    expect(screen.getByLabelText('Título')).toHaveValue('Título editado')
  })

  it('20 itens escondem Acrescentar mesmo com alvo livre; snapshot HTTP fora da faixa bloqueia salvar', async () => {
    const alvos = Array.from({ length: 21 }, (_, i) => alvo({ id: `alvo-${i + 1}`, name: `Serviço ${i + 1}` }))
    const itens = alvos.map((a) => ({ target_id: a.id, display_name: a.name }))
    const r = await montar({ pagina: pagina({ items: itens.slice(0, 20) }), alvos })
    expect(screen.queryByLabelText('Acrescentar alvo')).not.toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Tirar Serviço 20 da página' }))
    expect(await screen.findByLabelText('Acrescentar alvo')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Tirar Serviço 19 da página' })).toHaveFocus()
    // Nova página remonta o formulário. O decoder HTTP admite arrays sem
    // max; 21 itens não são uma saída normal do serviço Go atual.
    r.falsa.estado.paginas = [pagina({ id: 'snapshot-fora-da-faixa', items: itens })]
    await r.cliente.refetchQueries({ queryKey: ['status-pages'] })
    await screen.findByRole('group', { name: 'Alvos na página (21 de 20)' })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await screen.findByText('Até 20 alvos.')).toBeInTheDocument()
    expect(r.falsa.chamadas(PATCH)).toBe(0)
  })

  it('prévia usa resumo real e mostra o estado pausado; nome vazio é sinalizado sem enviar', async () => {
    const r = await montar({ pagina: comItem(), resumo: [itemDoResumo({ state: 'up', enabled: false })] })
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(await within(previa).findByText('Pausado')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Nome de exibição de API'), { target: { value: '' } })
    expect(within(previa).getByText('—')).toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(screen.getByLabelText('Nome de exibição de API')).toHaveAccessibleDescription('Obrigatório.'))
    expect(r.falsa.chamadas(PATCH)).toBe(0)
  })

  it('alvo que desaparece entre seleção e acréscimo não recebe nome inventado', async () => {
    const r = await montar({ alvos: [alvo(), alvo({ id: 'alvo-2', name: 'Site' })] })
    await r.usuario.selectOptions(screen.getByLabelText('Acrescentar alvo'), 'alvo-1')
    r.falsa.estado.alvos = [alvo({ id: 'alvo-2', name: 'Site' })]
    await r.cliente.refetchQueries({ queryKey: ['targets', 'list'] })
    await r.usuario.click(screen.getByRole('button', { name: 'Acrescentar' }))
    expect(await screen.findByLabelText('Nome de exibição de alvo apagado')).toHaveValue('')
    const previa = screen.getByRole('region', { name: 'Prévia aproximada' })
    expect(within(previa).getAllByText('—')).toHaveLength(2)
    expect(screen.getByRole('button', { name: 'Subir item' })).toHaveAttribute('aria-disabled', 'true')
    await r.usuario.click(screen.getByRole('button', { name: 'Subir item' }))
    expect(screen.getByRole('group', { name: 'Alvos na página (1 de 20)' })).toBeInTheDocument()
  })

  it('tirar o último alvo desaparecido leva foco ao título quando não há seletor', async () => {
    const r = await montar({ pagina: comItem(), alvos: [] })
    await r.usuario.click(screen.getByRole('button', { name: 'Tirar item da página' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Status page', level: 1 })).toHaveFocus())
    expect(screen.queryByLabelText('Acrescentar alvo')).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Prévia aproximada' })).not.toBeInTheDocument()
  })

  it('alvos carregando não impedem link público; erro HTTP permite tentar de novo e recuperar o formulário', async () => {
    const r = await montar({ pagina: pagina({ published: true }) })
    const p = respostaControlada()
    servidor.use(http.get('*/api/v1/targets', async () => {
      await p.liberada
      return HttpResponse.json({ error: { code: 'unavailable', message: 'genérica' }, trace_id: 'teste-status-page' }, { status: 503 })
    }, { once: true }))
    const leitura = r.cliente.resetQueries({ queryKey: ['targets', 'list'] })
    try {
      expect(await screen.findByText('Carregando…')).toBeInTheDocument()
      expect(screen.getByRole('link', { name: 'Abrir' })).toHaveAttribute('target', '_blank')
      expect(screen.queryByLabelText('Título')).not.toBeInTheDocument()
    } finally {
      p.soltar()
      await leitura
    }
    expect(await screen.findByRole('alert')).toHaveTextContent('Serviço indisponível no momento.')
    await r.usuario.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    expect(await screen.findByLabelText('Título')).toHaveValue('Minha')
  })

  it('tirar item e navegar com PATCH em voo desmonta a página; resposta não reabre o formulário', async () => {
    const r = await montar({ pagina: comItem(), alvos: [] })
    const p = respostaControlada()
    servidor.use(http.patch('*/api/v1/status-pages/:id', async () => {
      await p.liberada
      return HttpResponse.json(comItem())
    }))
    try {
      await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
      expect(screen.getByRole('button', { name: 'Salvar' })).toBeDisabled()
      await r.usuario.click(screen.getByRole('button', { name: 'Tirar item da página' }))
      expect(screen.getByRole('group', { name: 'Alvos na página (0 de 20)' })).toBeInTheDocument()
      await r.usuario.click(screen.getByRole('link', { name: 'Canais' }))
      expect(await screen.findByRole('heading', { name: 'Canais de notificação', level: 1 })).toBeInTheDocument()
      expect(screen.queryByLabelText('Título')).not.toBeInTheDocument()
    } finally {
      p.soltar()
    }
    const avisos = within(screen.getByRole('region', { name: 'Notifications (F8)' }))
    expect(await avisos.findByText('Status page salva. A página pública muda em até 30 segundos.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Título')).not.toBeInTheDocument()
  })
})
