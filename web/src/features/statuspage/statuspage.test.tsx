import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { alvo } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { sugerirSlug } from './PaginaStatusPage'

it('sugerirSlug tira acento e o que não cabe na regra', () => {
  expect(sugerirSlug('Situação da Ação — Produção!')).toBe('situacao-da-acao-producao')
  expect(sugerirSlug('  ')).toBe('')
})

describe('status page', () => {
  it('cria com alvos na ordem escolhida, e o slug ocupado cai no campo', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo(), alvo({ id: 'alvo-2', name: 'Site' })]
    await r.cliente.refetchQueries()
    await r.usuario.type(await screen.findByLabelText('Título'), 'Minha Empresa')
    await r.usuario.tab() // o blur sugere o slug
    expect(screen.getByLabelText('Endereço (slug)')).toHaveValue('minha-empresa')

    for (const nome of ['API', 'Site']) {
      await r.usuario.selectOptions(screen.getByLabelText('Acrescentar alvo'), nome)
      await r.usuario.click(screen.getByRole('button', { name: 'Acrescentar' }))
    }
    await r.usuario.click(screen.getByRole('button', { name: 'Subir Site' }))

    r.falsa.estado.forcar.set('POST /status-pages', { status: 409, code: 'slug_taken' })
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(screen.getByLabelText('Endereço (slug)')).toHaveAccessibleDescription(/já está em uso/))

    r.falsa.estado.forcar.delete('POST /status-pages')
    await r.usuario.click(screen.getByRole('button', { name: 'Criar status page' }))
    await waitFor(() => expect(r.falsa.chamadas('POST /status-pages')).toBe(2))
    expect(r.falsa.corpos('POST /status-pages')[1]).toEqual({
      title: 'Minha Empresa',
      slug: 'minha-empresa',
      published: false,
      items: [
        { target_id: 'alvo-2', display_name: 'Site' },
        { target_id: 'alvo-1', display_name: 'API' },
      ],
    })
    // Rascunho: o link existe, mas a página ainda não abre.
    expect(await screen.findByText('A página só fica acessível depois de publicada.')).toBeInTheDocument()
  })

  it('a prévia é rotulada como aproximada', async () => {
    const r = await renderizarPainel('/status-pages')
    r.falsa.estado.alvos = [alvo()]
    r.falsa.estado.paginas = [
      {
        id: 'pagina-1',
        slug: 'minha',
        title: 'Minha',
        published: true,
        items: [{ target_id: 'alvo-1', display_name: 'API pública' }],
        public_url: 'http://localhost:5173/p/minha',
        created_at: '2026-09-20T10:00:00Z',
      },
    ]
    await r.cliente.refetchQueries()
    const previa = await screen.findByRole('region', { name: 'Prévia aproximada' })
    expect(within(previa).getByText('API pública')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Abrir' })).toHaveAttribute('rel', 'noopener noreferrer')
  })
})
