import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderizarPainel } from '@/test/renderizar'

// Os Termos de Uso e a Política de Privacidade: públicos, como o cadastro que
// leva até eles, e sem nome nem CNPJ de empresa até o dono aprovar o texto.
const PAGINAS = [
  { rota: '/termos', titulo: 'Termos de Uso' },
  { rota: '/privacidade', titulo: 'Política de Privacidade' },
] as const

const CNPJ = /\d{2}\.\d{3}\.\d{3}\/\d{4}-\d{2}/

describe('páginas legais', () => {
  // Sabotagem prevista: pôr as rotas dentro do ExigirSessao (sem sessão, a
  // tela vira o login).
  it.each(PAGINAS)('páginas legais abrem sem sessão: $rota', async ({ rota, titulo }) => {
    const { falsa } = await renderizarPainel(rota, { logado: false })
    expect(await screen.findByRole('heading', { name: titulo, level: 1 })).toBeInTheDocument()
    expect(document.title).toBe(`${titulo} — StillOn`)
    expect(falsa.chamadas('GET /me')).toBe(0)
    const main = screen.getByRole('main')
    expect(main).toHaveTextContent('Versão 2026-11-28')
    expect(within(main).getByRole('link', { name: 'Criar conta' })).toHaveAttribute('href', '/signup')
  })

  // Sabotagem prevista: colar um CNPJ de exemplo no texto.
  it.each(PAGINAS)('textos legais não citam empresa: $rota', async ({ rota, titulo }) => {
    await renderizarPainel(rota, { logado: false })
    await screen.findByRole('heading', { name: titulo, level: 1 })
    const texto = screen.getByRole('main').textContent ?? ''
    expect(texto).toContain('[operador]')
    expect(texto).toContain('[contato do operador]')
    expect(texto).not.toMatch(CNPJ)
  })
})
