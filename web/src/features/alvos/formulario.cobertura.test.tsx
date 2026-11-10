import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
// Pré-carrega a rota lazy na coleta, que não tem prazo: salvar leva ao
// detalhe, e o import (Recharts) não pode correr contra o findBy nem o teste.
import '@/features/alvos/PaginaDetalhe'
import '@/test/aquecer-estilos'

const PATCH = 'PATCH /targets/:id'

async function montarEdicao() {
  const r = await renderizarPainel('/targets/alvo-1/edit')
  r.falsa.estado.alvos = [alvo({ header_names: ['Authorization', 'X-Original'] })]
  await r.cliente.refetchQueries()
  await screen.findByText('Headers definidos: Authorization, X-Original (valores ocultos).')
  return r
}

describe('formulário de alvo: contratos públicos complementares', () => {
  it('acrescentar e remover headers renumera a linha e envia apenas o valor remanescente editado', async () => {
    const r = await montarEdicao()
    expect(screen.queryByLabelText('Nome do header 1')).not.toBeInTheDocument()
    await r.usuario.click(screen.getByLabelText('Substituir todos'))
    await r.usuario.click(screen.getByRole('button', { name: 'Adicionar header' }))
    expect(await screen.findByLabelText('Nome do header 1')).toHaveValue('')
    expect(screen.getByLabelText('Valor do header 1')).toHaveValue('')
    await r.usuario.type(screen.getByLabelText('Nome do header 1'), 'X-Primeiro')
    await r.usuario.type(screen.getByLabelText('Valor do header 1'), 'primeiro')
    await r.usuario.click(screen.getByRole('button', { name: 'Adicionar header' }))
    await r.usuario.type(await screen.findByLabelText('Nome do header 2'), 'X-Segundo')
    await r.usuario.type(screen.getByLabelText('Valor do header 2'), 'segundo')
    await r.usuario.click(screen.getByRole('button', { name: 'Remover o header 1' }))
    expect(screen.queryByLabelText('Nome do header 2')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Nome do header 1')).toHaveValue('X-Segundo')
    expect(screen.getByLabelText('Valor do header 1')).toHaveValue('segundo')
    expect(screen.getAllByRole('button', { name: /Remover o header/ })).toHaveLength(1)
    await r.usuario.clear(screen.getByLabelText('Valor do header 1'))
    await r.usuario.type(screen.getByLabelText('Valor do header 1'), 'atualizado')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos(PATCH)).toEqual([{ headers: { 'X-Segundo': 'atualizado' } }]))
    expect(await screen.findByRole('heading', { name: 'API', level: 1 })).toBeInTheDocument()
    expect(r.falsa.estado.alvos[0]!.header_names).toEqual(['X-Segundo'])
  })

  it('field HTTP desconhecido vira aviso geral e conserva a edição, sem marcar um controle alheio', async () => {
    const r = await montarEdicao()
    await r.usuario.clear(screen.getByLabelText('Nome'))
    await r.usuario.type(screen.getByLabelText('Nome'), 'API editada')
    // O decoder admite campos novos. Este field não é uma resposta normal
    // de validação do serviço Go atual; é um contrato defensivo HTTP.
    r.falsa.estado.forcar.set(PATCH, {
      status: 400, code: 'validation_failed',
      fields: [{ field: 'campo_futuro', reason: 'invalid' }],
    })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Confira os campos destacados.')
    expect(screen.getByLabelText('Nome')).toHaveValue('API editada')
    expect(screen.getByLabelText('Nome')).not.toHaveAttribute('aria-invalid')
    expect(screen.getByLabelText('URL')).not.toHaveAttribute('aria-invalid')
    expect(screen.getByRole('heading', { name: 'Editar alvo', level: 1 })).toBeInTheDocument()
    expect(r.falsa.corpos(PATCH)).toEqual([{ name: 'API editada' }])
  })
})

describe('formulário de alvo: regra de conteúdo em "Avançado"', () => {
  const VERIFICACAO = 'Verificação'
  const DEVE_APARECER = 'Texto que deve aparecer'
  const TEXTO_DO_HEAD = 'Com HEAD a resposta não tem corpo. Escolha GET ou POST para verificar o conteúdo.'
  const TEXTO_DO_ACCEPT =
    'Remova o header Accept-Encoding para verificar o conteúdo: a resposta viria comprimida.'
  const TEXTO_DO_RANGE = 'Remova o header Range para verificar o conteúdo: a resposta viria cortada.'
  const COM_REGRA = { content_rule: { mode: 'contains' as const, value: 'ok', json_pointer: null } }

  async function montarCriacao() {
    const r = await renderizarPainel('/targets/new')
    await r.usuario.type(await screen.findByLabelText('Nome'), 'Site')
    await r.usuario.type(screen.getByLabelText('URL'), 'https://exemplo.com')
    return r
  }

  async function montarEdicaoCom(p: Parameters<typeof alvo>[0]) {
    const r = await renderizarPainel('/targets/alvo-1/edit')
    r.falsa.estado.alvos = [alvo(p)]
    await r.cliente.refetchQueries()
    await screen.findByRole('heading', { name: 'Editar alvo', level: 1 })
    await screen.findByLabelText('Nome')
    return r
  }

  function avancado() {
    const details = screen.getByText('Avançado').closest('details')
    if (!details) throw new Error('"Avançado" fora de um <details>')
    return details
  }

  it('"Avançado" começa fechado, com a regra desligada', async () => {
    await montarCriacao()
    expect(avancado()).not.toHaveAttribute('open')
    expect(screen.getByRole('group', { name: 'Verificar o conteúdo da resposta' })).toBeInTheDocument()
    expect(screen.getByLabelText(VERIFICACAO)).toHaveValue('desligada')
    expect(screen.queryByLabelText(DEVE_APARECER)).not.toBeInTheDocument()
  })

  // L8 do STRIDE: o valor fica em claro no banco e no backup. O aviso está na
  // descrição do campo, para quem chega a ele pelo Tab.
  it('o campo do valor avisa para não usar segredo na regra', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    expect(screen.getByLabelText(DEVE_APARECER)).toHaveAccessibleDescription(
      'Não use segredo na regra: o valor fica guardado sem criptografia, no banco e no backup.',
    )
  })

  it.each([
    ['Contém', 'Texto que deve aparecer'],
    ['Não contém', 'Texto que não pode aparecer'],
    ['JSON igual a', 'Valor esperado'],
  ])('o modo %s chama o valor de "%s"', async (modo, rotulo) => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), modo)
    expect(screen.getByLabelText(rotulo)).toBeInTheDocument()
  })

  it('o seletor tem rótulo, as opções da spec e alvo de toque de 44 px', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    const seletor = screen.getByLabelText(VERIFICACAO)
    expect(seletor).toHaveClass('min-h-11')
    expect(within(seletor).getAllByRole('option').map((o) => o.textContent)).toEqual([
      'Desligada', 'Contém', 'Não contém', 'JSON igual a',
    ])
    expect(seletor).toHaveAccessibleDescription('Compara bytes em UTF-8 nos primeiros 64 KiB da resposta.')
  })

  // M4 da revisão: a ajuda dos números fala do valor, não do ponteiro.
  it('em JSON igual a, a ajuda dos números fica no valor', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'JSON igual a')
    expect(screen.getByLabelText('Valor esperado')).toHaveAccessibleDescription(
      expect.stringContaining('Números são comparados como escritos no JSON.'),
    )
    expect(screen.getByLabelText('Ponteiro JSON')).not.toHaveAccessibleDescription(expect.stringContaining('Números'))
  })

  it('cria com a regra JSON e manda o ponteiro', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'JSON igual a')
    await r.usuario.type(screen.getByLabelText('Valor esperado'), 'up')
    await r.usuario.type(screen.getByLabelText('Ponteiro JSON'), '/status')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() =>
      expect(r.falsa.corpos('POST /targets')[0]).toMatchObject({
        content_rule: { mode: 'json_equals', value: 'up', json_pointer: '/status' },
      }),
    )
  })

  it('com HEAD o seletor fica desabilitado, explica por quê e a regra não vai', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.selectOptions(screen.getByLabelText('Método'), 'HEAD')
    const seletor = screen.getByLabelText(VERIFICACAO)
    expect(seletor).toBeDisabled()
    expect(seletor).toHaveValue('desligada')
    expect(seletor).toHaveAccessibleDescription(TEXTO_DO_HEAD)
    expect(screen.queryByLabelText(DEVE_APARECER)).not.toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(r.falsa.corpos('POST /targets')).toHaveLength(1))
    expect(r.falsa.corpos('POST /targets')[0]).toMatchObject({ method: 'HEAD' })
    expect(r.falsa.corpos('POST /targets')[0]).not.toHaveProperty('content_rule')
  })

  // I1 da revisão. Sabotagem: desligar a regra no onChange do método → a
  // regra volta "Desligada" e não vai no POST.
  it('na criação, HEAD e volta a GET mostra a regra como estava e a envia', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.selectOptions(screen.getByLabelText('Método'), 'HEAD')
    await r.usuario.selectOptions(screen.getByLabelText('Método'), 'GET')
    expect(screen.getByLabelText(VERIFICACAO)).toBeEnabled()
    expect(screen.getByLabelText(VERIFICACAO)).toHaveValue('contains')
    expect(screen.getByLabelText(DEVE_APARECER)).toHaveValue('ok')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() =>
      expect(r.falsa.corpos('POST /targets')[0]).toMatchObject({
        method: 'GET', content_rule: { mode: 'contains', value: 'ok' },
      }),
    )
  })

  it('na edição, HEAD e volta a GET não apaga a regra guardada', async () => {
    const r = await montarEdicaoCom(COM_REGRA)
    await r.usuario.selectOptions(screen.getByLabelText('Método'), 'HEAD')
    await r.usuario.selectOptions(screen.getByLabelText('Método'), 'GET')
    expect(screen.getByLabelText(VERIFICACAO)).toHaveValue('contains')
    await r.usuario.clear(screen.getByLabelText('Nome'))
    await r.usuario.type(screen.getByLabelText('Nome'), 'API editada')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos(PATCH)).toEqual([{ name: 'API editada' }]))
    expect(r.falsa.estado.alvos[0]!.content_rule).toEqual({ mode: 'contains', value: 'ok', json_pointer: null })
  })

  it('na edição, salvar com HEAD manda content_rule: null, que o servidor exige', async () => {
    const r = await montarEdicaoCom(COM_REGRA)
    await r.usuario.selectOptions(screen.getByLabelText('Método'), 'HEAD')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos(PATCH)).toEqual([{ method: 'HEAD', content_rule: null }]))
  })

  it('com o header Accept-Encoding, o envio acusa o seletor sem apagar o digitado', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByRole('button', { name: 'Adicionar header' }))
    await r.usuario.type(await screen.findByLabelText('Nome do header 1'), 'accept-encoding')
    await r.usuario.type(screen.getByLabelText('Valor do header 1'), 'gzip')
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    const seletor = screen.getByLabelText(VERIFICACAO)
    await waitFor(() => expect(seletor).toHaveAttribute('aria-invalid', 'true'))
    expect(seletor).toHaveAccessibleDescription(expect.stringContaining(TEXTO_DO_ACCEPT))
    expect(screen.getByLabelText('Nome do header 1')).toHaveValue('accept-encoding')
    expect(screen.getByLabelText(DEVE_APARECER)).toHaveValue('ok')
    expect(r.falsa.corpos('POST /targets')).toEqual([])
  })

  it.each(['range', 'RANGE'])('com o header %s, o envio acusa o seletor e abre "Avançado"', async (nome) => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByRole('button', { name: 'Adicionar header' }))
    await r.usuario.type(await screen.findByLabelText('Nome do header 1'), nome)
    await r.usuario.type(screen.getByLabelText('Valor do header 1'), 'bytes=0-99')
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.click(screen.getByText('Avançado'))
    expect(avancado()).not.toHaveAttribute('open')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    const seletor = screen.getByLabelText(VERIFICACAO)
    await waitFor(() => expect(seletor).toHaveFocus())
    expect(avancado()).toHaveAttribute('open')
    expect(seletor).toHaveAttribute('aria-invalid', 'true')
    expect(seletor).toHaveAccessibleDescription(expect.stringContaining(TEXTO_DO_RANGE))
    expect(screen.getByLabelText('Nome do header 1')).toHaveValue(nome)
    expect(r.falsa.corpos('POST /targets')).toEqual([])
  })

  it('na edição, um Range guardado acusa o seletor antes do PATCH', async () => {
    const r = await montarEdicaoCom({ header_names: ['Range'] })
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const seletor = screen.getByLabelText(VERIFICACAO)
    await waitFor(() => expect(seletor).toHaveAttribute('aria-invalid', 'true'))
    expect(seletor).toHaveAccessibleDescription(expect.stringContaining(TEXTO_DO_RANGE))
    expect(r.falsa.corpos(PATCH)).toEqual([])
  })

  // M5 da revisão: com "Manter como está", o Accept-Encoding guardado já é
  // acusado no cliente, pelos nomes que a edição conhece.
  it('na edição, um Accept-Encoding guardado acusa o seletor antes do PATCH', async () => {
    const r = await montarEdicaoCom({ header_names: ['Accept-Encoding'] })
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const seletor = screen.getByLabelText(VERIFICACAO)
    await waitFor(() => expect(seletor).toHaveAttribute('aria-invalid', 'true'))
    expect(seletor).toHaveAccessibleDescription(expect.stringContaining(TEXTO_DO_ACCEPT))
    expect(r.falsa.corpos(PATCH)).toEqual([])
  })

  // N1 da re-revisão: com HEAD a regra não vale, então o erro dela não fica
  // no seletor travado.
  it('trocar para HEAD tira o erro já mostrado no seletor da regra', async () => {
    const r = await montarEdicaoCom({ header_names: ['Accept-Encoding'] })
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(screen.getByLabelText(VERIFICACAO)).toHaveAttribute('aria-invalid', 'true'))
    await r.usuario.selectOptions(screen.getByLabelText('Método'), 'HEAD')
    const travado = screen.getByLabelText(VERIFICACAO)
    expect(travado).toBeDisabled()
    expect(travado).not.toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByText(TEXTO_DO_ACCEPT, { exact: false })).not.toBeInTheDocument()
  })

  it('o erro da regra no envio abre "Avançado" e leva o foco ao campo', async () => {
    const r = await montarCriacao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.click(screen.getByText('Avançado'))
    expect(avancado()).not.toHaveAttribute('open')
    await r.usuario.click(screen.getByRole('button', { name: 'Cadastrar' }))
    await waitFor(() => expect(screen.getByLabelText(DEVE_APARECER)).toHaveFocus())
    expect(avancado()).toHaveAttribute('open')
    expect(screen.getByLabelText(DEVE_APARECER)).toHaveAccessibleDescription(expect.stringContaining('Obrigatório.'))
  })

  it('o 400 content_rule forbidden do servidor cai no seletor da regra', async () => {
    const r = await montarEdicao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Contém')
    await r.usuario.type(screen.getByLabelText(DEVE_APARECER), 'ok')
    await r.usuario.click(screen.getByText('Avançado'))
    r.falsa.estado.forcar.set(PATCH, {
      status: 400, code: 'validation_failed',
      fields: [{ field: 'content_rule', reason: 'forbidden' }],
    })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const seletor = screen.getByLabelText(VERIFICACAO)
    await waitFor(() => expect(seletor).toHaveAttribute('aria-invalid', 'true'))
    expect(seletor).toHaveAccessibleDescription(
      expect.stringContaining('Para verificar o conteúdo, use GET ou POST e tire os headers Accept-Encoding e Range.'),
    )
    expect(avancado()).toHaveAttribute('open')
    expect(seletor).toHaveFocus()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByLabelText(DEVE_APARECER)).toHaveValue('ok')
  })

  // M9 da revisão: o 400 de um campo de dentro da regra chega ao próprio campo.
  it('o 400 content_rule.value do servidor cai no campo do valor, com a seção aberta', async () => {
    const r = await montarEdicao()
    await r.usuario.click(screen.getByText('Avançado'))
    await r.usuario.selectOptions(screen.getByLabelText(VERIFICACAO), 'Não contém')
    await r.usuario.type(screen.getByLabelText('Texto que não pode aparecer'), 'erro')
    await r.usuario.click(screen.getByText('Avançado'))
    r.falsa.estado.forcar.set(PATCH, {
      status: 400, code: 'validation_failed',
      fields: [{ field: 'content_rule.value', reason: 'too_long' }],
    })
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    const valor = screen.getByLabelText('Texto que não pode aparecer')
    await waitFor(() => expect(valor).toHaveAttribute('aria-invalid', 'true'))
    expect(valor).toHaveAccessibleDescription(expect.stringContaining('Até 256 caracteres.'))
    expect(avancado()).toHaveAttribute('open')
    expect(valor).toHaveFocus()
    expect(screen.getByLabelText(VERIFICACAO)).not.toHaveAttribute('aria-invalid')
  })

  it('na edição, um alvo com regra abre "Avançado" e desligar manda content_rule: null', async () => {
    const r = await montarEdicaoCom(COM_REGRA)
    const seletor = await screen.findByLabelText(VERIFICACAO)
    await waitFor(() => expect(seletor).toHaveValue('contains'))
    expect(avancado()).toHaveAttribute('open')
    expect(screen.getByLabelText(DEVE_APARECER)).toHaveValue('ok')
    await r.usuario.selectOptions(seletor, 'Desligada')
    await r.usuario.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => expect(r.falsa.corpos(PATCH)).toEqual([{ content_rule: null }]))
    expect(r.falsa.estado.alvos[0]!.content_rule).toBeNull()
  })
})
