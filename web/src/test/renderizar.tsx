import { onlineManager } from '@tanstack/react-query'
import { act, render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode, useEffect, type ReactNode } from 'react'
import { createBrowserRouter, createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { criarClienteDeConsultas, ProvedorDoPainel } from '@/app/contexto'
import { rotas } from '@/app/rotas'
import { esquecerAPrimeiraTela } from '@/components/Titulo'
import { chaves } from '@/lib/api/consultas'
import { ProvedorDeAvisos } from '@/components/ui/avisos'
import { ProvedorDeTema } from '@/lib/tema'
import { EMAIL, SENHA, type ApiFalsa } from './api-falsa'
import { buscarDoTeste, novaSessao, usarApiFalsa } from './ambiente'

function EscopoDaFixture({ encerrar, children }: { encerrar: () => void; children: ReactNode }) {
  useEffect(() => encerrar, [encerrar])
  return children
}

// Monta o painel inteiro (rotas, provedores, sessão) numa rota, contra a API
// falsa. logado: o navegador tem um cookie de refresh válido, e o boot entra.
// atrasoDoBoot: quanto o refresh do boot demora (para ver o splash).
// prazoDoConfirmMs: o prazo dos confirms do 2FA (30 s no painel).
// navegador: o router do painel de verdade, sobre window.history; o de memória
// não escreve lá, e um teste do histórico passaria sem testar nada.
// antes: prepara a API falsa antes da primeira consulta (ex.: um projeto que a
// lista de projetos do boot já precisa trazer).
// esperarARaiz: false devolve a fixture sem esperar a lista de projetos (para
// o teste que segura o GET /projects).
export async function renderizarPainel(
  rota: string,
  o: {
    logado?: boolean
    atrasoDoBoot?: number
    prazoDoConfirmMs?: number
    navegador?: boolean
    antes?: (falsa: ApiFalsa) => void
    esperarARaiz?: boolean
  } = {},
) {
  // Cada render é uma carga de página nova: a primeira tela não recebe foco.
  esquecerAPrimeiraTela()
  const falsa = usarApiFalsa()
  if (o.logado ?? true) falsa.logado()
  falsa.estado.atrasoRefresh = o.atrasoDoBoot ?? 0
  o.antes?.(falsa)
  const vida = new AbortController()
  const { sessao, api } = novaSessao({
    prazoDoConfirmMs: o.prazoDoConfirmMs,
    buscar: (entrada, init) => buscarDoTeste(entrada, {
      ...init,
      signal: init.signal ? AbortSignal.any([vida.signal, init.signal]) : vida.signal,
    }),
  })
  const cliente = criarClienteDeConsultas()
  // Nos testes, erro é erro: sem as tentativas que escondem um 500.
  cliente.setDefaultOptions({
    ...cliente.getDefaultOptions(),
    queries: { ...cliente.getDefaultOptions().queries, retry: false },
  })
  if (o.navegador) window.history.replaceState(null, '', rota)
  const router = o.navegador ? createBrowserRouter(rotas) : createMemoryRouter(rotas, { initialEntries: [rota] })
  const usuario = userEvent.setup()
  const encerrar = () => {
    vida.abort(new DOMException('A fixture foi desmontada.', 'AbortError'))
    router.dispose()
    cliente.clear()
    if (o.navegador) window.history.replaceState(null, '', '/')
  }
  const tela = render(
    // O escopo fica fora do StrictMode: o cleanup de desenvolvimento dos
    // filhos não encerra uma carga de página ainda montada.
    <EscopoDaFixture encerrar={encerrar}>
      <StrictMode>
        <ProvedorDoPainel sessao={sessao} api={api} cliente={cliente}>
          <ProvedorDeTema>
            <ProvedorDeAvisos>
              <RouterProvider router={router} />
            </ProvedorDeAvisos>
          </ProvedorDeTema>
        </ProvedorDoPainel>
      </StrictMode>
    </EscopoDaFixture>,
  )
  await act(async () => {
    const boot = sessao.iniciar()
    if (!o.atrasoDoBoot) await boot
    // O boot da sessão pode terminar antes do import da primeira rota lazy.
    // idle não sinaliza prontidão nessa navegação; initialized sinaliza.
    if (!router.state.initialized) {
      await new Promise<void>((resolve) => {
        const parar = router.subscribe((estado) => {
          if (estado.initialized) {
            parar()
            resolve()
          }
        })
      })
    }
  })
  // A raiz e os links de antes dos projetos só redirecionam quando a lista de
  // projetos chega. Uma carga de página termina no destino, como a pessoa a
  // vê antes de clicar: um router.navigate sem espera (sem estado loading)
  // logo depois do render ainda perderia para o redirecionamento atrasado.
  // A navegação em espera não perde: carga-de-rota.test.tsx prova, com esta
  // espera desligada.
  // Sem rede, a consulta fica pausada: o teste decide quando ela volta.
  if ((o.esperarARaiz ?? true) && sessao.estado() === 'autenticado' && onlineManager.isOnline()) {
    await act(async () => {
      await cliente
        .ensureQueryData({ queryKey: chaves.projetos, queryFn: ({ signal }) => api.projetos(signal) })
        .catch(() => undefined)
    })
    await act(() => new Promise<void>((resolve) => setTimeout(resolve, 0)))
  }
  return { falsa, sessao, api, cliente, router, usuario, tela }
}

export { EMAIL, SENHA }
