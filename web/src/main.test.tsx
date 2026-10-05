import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { getNonce, setNonce } from 'get-nonce'
import type { Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { EMAIL } from '@/test/api-falsa'
import { usarApiFalsa } from '@/test/ambiente'

const { roots } = vi.hoisted(() => ({ roots: [] as Root[] }))
const canais: BroadcastChannel[] = []
let nonceAnterior: string | undefined
let tituloAnterior = ''
let caminhoAnterior = '/'

// O namespace ESM não aceita spyOn; a substituição conserva todo o módulo
// real e apenas guarda seus roots para desmontá-los ao final.
vi.mock('react-dom/client', async (importOriginal) => {
  const real = await importOriginal<typeof import('react-dom/client')>()
  return {
    ...real,
    createRoot: (...args: Parameters<typeof real.createRoot>) => {
      const root = real.createRoot(...args)
      roots.push(root)
      return root
    },
  }
})

afterEach(async () => {
  await act(() => roots.forEach((root) => root.unmount()))
  roots.length = 0
  canais.forEach((canal) => canal.close())
  canais.length = 0
  vi.unstubAllGlobals()
  document.getElementById('root')?.remove()
  document.head.querySelector('meta[property="csp-nonce"]')?.remove()
  document.documentElement.classList.remove('dark')
  document.title = tituloAnterior
  history.replaceState(null, '', caminhoAnterior)
  setNonce(nonceAnterior ?? '')
})

// Sabotagens: remover o iniciar() deixa o splash preso; remover um provider
// perde a API, a persistência do tema ou o aviso; remover o import de nonce
// deixa o estilo real do diálogo sem o nonce da resposta.
it('o bootstrap real reidrata a sessão e entrega tema, avisos e estilos autorizados pela CSP', async () => {
  nonceAnterior = getNonce()
  tituloAnterior = document.title
  caminhoAnterior = location.pathname + location.search + location.hash
  const falsa = usarApiFalsa()
  falsa.logado()
  history.replaceState(null, '', '/channels')
  localStorage.setItem('stillon.tema', 'escuro')
  const meta = document.createElement('meta')
  meta.setAttribute('property', 'csp-nonce')
  meta.nonce = 'nonce-do-bootstrap-456'
  document.head.append(meta)
  const container = document.createElement('div')
  container.id = 'root'
  document.body.append(container)

  // O Node não resolve fetch relativo; a adaptação conserva RequestInit e
  // delega ao fetch real interceptado pelo MSW, como o navegador faria.
  const buscar = globalThis.fetch
  vi.stubGlobal('fetch', (entrada: RequestInfo | URL, init?: RequestInit) =>
    buscar(typeof entrada === 'string' ? new URL(entrada, location.href) : entrada, init))
  const CanalReal = BroadcastChannel
  vi.stubGlobal('BroadcastChannel', class extends CanalReal {
    constructor(nome: string) {
      super(nome)
      canais.push(this)
    }
  })

  await act(async () => { await import('./main') })

  expect(await screen.findByRole('heading', { name: 'Canais de notificação' })).toBeInTheDocument()
  expect(await screen.findByText(EMAIL)).toBeInTheDocument()
  expect(screen.getByRole('combobox', { name: 'Tema' })).toHaveValue('escuro')
  expect(document.documentElement).toHaveClass('dark')

  const usuario = userEvent.setup()
  await usuario.selectOptions(screen.getByRole('combobox', { name: 'Tema' }), 'claro')
  expect(document.documentElement).not.toHaveClass('dark')
  expect(localStorage.getItem('stillon.tema')).toBe('claro')

  await usuario.click(screen.getByRole('button', { name: 'Novo canal' }))
  const dialogo = await screen.findByRole('dialog', { name: 'Novo canal' })
  await waitFor(() => expect(document.body).toHaveAttribute('data-scroll-locked'))
  const estilo = [...document.head.querySelectorAll('style')]
    .find((el) => el.textContent?.includes('body[data-scroll-locked]'))
  expect(estilo?.nonce).toBe('nonce-do-bootstrap-456')

  await usuario.click(within(dialogo).getByRole('radio', { name: 'E-mail' }))
  await usuario.type(within(dialogo).getByRole('textbox', { name: 'E-mail' }), 'avisos@exemplo.com')
  await usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
  expect(await screen.findByText('Enviamos o link de verificação.')).toBeInTheDocument()

  // StrictMode e uma segunda importação do entrypoint não gastam outro cookie.
  await import('./main')
  expect(falsa.estado.chamadas.get('POST /auth/refresh')).toBe(1)
})
