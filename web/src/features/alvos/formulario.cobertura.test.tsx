import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

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
