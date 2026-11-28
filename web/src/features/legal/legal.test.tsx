import { createHash } from 'node:crypto'
import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { chaves } from '@/lib/api/consultas'
import { renderizarPainel } from '@/test/renderizar'
import versaoDosTermos from './versao.json'

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

  // Até o 6.6, as páginas prometem recursos que ainda não existem e mostram os
  // marcadores do operador: só aparecem com o cadastro aberto. Duas
  // sabotagens previstas: tirar a porta (o fechado mostra o texto) e trocar
  // "=== true" por "!== false" (a config que falhou mostra).
  it.each(PAGINAS.flatMap((p) => (['desligado', 'falhando'] as const).map((caso) => ({ ...p, caso }))))(
    'com o cadastro $caso, $rota é página não encontrada',
    async ({ rota, caso }) => {
      const { cliente } = await renderizarPainel(rota, {
        logado: false,
        antes: (falsa) => {
          if (caso === 'desligado') falsa.estado.cadastro.habilitado = false
          else falsa.estado.forcar.set('GET /auth/signup-config', { status: 500, code: 'internal_error' })
        },
      })
      expect(await screen.findByRole('heading', { name: 'Página não encontrada', level: 1 })).toBeInTheDocument()
      expect(cliente.getQueryState(chaves.configDoCadastro)?.status).toBe(caso === 'desligado' ? 'success' : 'error')
      const texto = document.body.textContent ?? ''
      expect(texto).not.toContain('[operador]')
      expect(texto).not.toContain('Versão ')
      expect(screen.queryByRole('link', { name: 'Criar conta' })).not.toBeInTheDocument()
    },
  )

  // Sabotagem prevista: renderizar o texto enquanto a config não chega.
  it.each(PAGINAS)('enquanto a config carrega, $rota não mostra o texto', async ({ rota, titulo }) => {
    const { sessao } = await renderizarPainel(rota, { logado: false, atrasoDoBoot: 100 })
    expect(screen.getByText('Carregando…')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('[operador]')
    await waitFor(() => expect(sessao.estado()).toBe('anonimo'))
    expect(await screen.findByRole('heading', { name: titulo, level: 1 })).toBeInTheDocument()
  })

  // O alvo-de-toque cresce o link por um ::after de 44 px, que em prosa cobre
  // as linhas vizinhas: o link no meio do texto fica inline, e o do rodapé
  // continua com ele. Sabotagem prevista: voltar ao LinkTerciario no texto.
  it.each([
    { rota: '/termos', titulo: 'Termos de Uso', link: 'Política de Privacidade', para: '/privacidade' },
    { rota: '/privacidade', titulo: 'Política de Privacidade', link: 'Termos de Uso', para: '/termos' },
  ])('o link no texto de $rota não tem alvo de toque', async ({ rota, titulo, link, para }) => {
    await renderizarPainel(rota, { logado: false })
    await screen.findByRole('heading', { name: titulo, level: 1 })
    const noTexto = screen.getByRole('link', { name: link })
    expect(noTexto).toHaveAttribute('href', para)
    expect(noTexto).not.toHaveClass('alvo-de-toque')
    expect(screen.getByRole('link', { name: 'Entrar' })).toHaveClass('alvo-de-toque')
  })

  // O texto e a versão andam juntos: o servidor (legal.VersaoAtual) só aceita
  // o cadastro com a versão do versao.json. Sabotagem prevista: mudar uma
  // palavra do texto sem mudar a versão.
  it.each([
    { rota: '/termos', titulo: 'Termos de Uso', hash: HASH_DOS_TEXTOS.termos },
    { rota: '/privacidade', titulo: 'Política de Privacidade', hash: HASH_DOS_TEXTOS.privacidade },
  ])('o texto de $rota é o da versão vigente', async ({ rota, titulo, hash }) => {
    await renderizarPainel(rota, { logado: false })
    await screen.findByRole('heading', { name: titulo, level: 1 })
    const texto = screen.getByRole('main').textContent ?? ''
    const atual = { versao: versaoDosTermos.versao, hash: createHash('sha256').update(texto).digest('hex') }
    expect(
      atual,
      'o texto mudou: troque a versão em versao.json e em legal.VersaoAtual e atualize o hash',
    ).toEqual({ versao: HASH_DOS_TEXTOS.versao, hash })
  })
})

// O SHA-256 do texto renderizado (o <main> inteiro) de cada página, na versão
// em que foi aprovado.
const HASH_DOS_TEXTOS = {
  versao: '2026-11-28',
  termos: 'a61b4e8742e392d845c8731989938b30aee6cbacde06d7b9927fb04848982c6e',
  privacidade: '9c7fa7769e040d56a223dd99b03caeace08459171413e87db42b671d6573bfa1',
}
