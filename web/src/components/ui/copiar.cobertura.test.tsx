import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { pagina } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'

const FALHA = 'Não foi possível copiar. Selecione o link e copie à mão.'

async function montarLinkPublico() {
  const r = await renderizarPainel('/status-pages')
  r.falsa.estado.paginas = [pagina()]
  await r.cliente.refetchQueries({ queryKey: ['status-pages'] })
  const botao = await screen.findByRole('button', { name: 'Copiar' })
  return { ...r, botao }
}

describe('copiar: ausência pública da capacidade de clipboard', () => {
  it('sem clipboard, avisa uma vez e conserva o rótulo Copiar', async () => {
    const r = await montarLinkPublico()
    const descriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
    try {
      // Simula a API pública ausente; jsdom não prova políticas de contexto seguro.
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
      await r.usuario.click(r.botao)
      const avisos = within(screen.getByRole('region', { name: 'Notifications (F8)' }))
      expect(await avisos.findAllByText(FALHA)).toHaveLength(1)
      expect(avisos.queryByText('Link copiado.')).not.toBeInTheDocument()
      expect(r.botao).toHaveTextContent(/^Copiar$/)
      expect(screen.queryByRole('button', { name: 'Copiado' })).not.toBeInTheDocument()
    } finally {
      if (descriptor) Object.defineProperty(navigator, 'clipboard', descriptor)
      else Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  it('ausência logo após sucesso devolve Copiar e não confirma uma nova cópia', async () => {
    const r = await montarLinkPublico()
    const descriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
    try {
      await r.usuario.click(r.botao)
      const avisos = within(screen.getByRole('region', { name: 'Notifications (F8)' }))
      expect(await avisos.findAllByText('Link copiado.')).toHaveLength(1)
      expect(r.botao).toHaveTextContent(/^Copiado$/)
      expect(await navigator.clipboard.readText()).toBe('http://localhost/p/minha')
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
      await r.usuario.click(r.botao)
      expect(await avisos.findAllByText(FALHA)).toHaveLength(1)
      expect(avisos.getAllByText('Link copiado.')).toHaveLength(1)
      expect(r.botao).toHaveTextContent(/^Copiar$/)
    } finally {
      if (descriptor) Object.defineProperty(navigator, 'clipboard', descriptor)
      else Reflect.deleteProperty(navigator, 'clipboard')
    }
  })
})
