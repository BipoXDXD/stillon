import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { criarClienteDeConsultas, ProvedorDoPainel } from '@/app/contexto'
import { rotas } from '@/app/rotas'
import { esquecerAPrimeiraTela } from '@/components/Titulo'
import { ProvedorDeAvisos } from '@/components/ui/avisos'
import { ProvedorDeTema } from '@/lib/tema'
import { EMAIL, SENHA } from './api-falsa'
import { novaSessao, usarApiFalsa } from './ambiente'

// Monta o painel inteiro (rotas, provedores, sessão) numa rota, contra a API
// falsa. logado: o navegador tem um cookie de refresh válido, e o boot entra.
// atrasoDoBoot: quanto o refresh do boot demora (para ver o splash).
// prazoDoConfirmMs: o prazo dos confirms do 2FA (30 s no painel).
export async function renderizarPainel(
  rota: string,
  o: { logado?: boolean; atrasoDoBoot?: number; prazoDoConfirmMs?: number } = {},
) {
  // Cada render é uma carga de página nova: a primeira tela não recebe foco.
  esquecerAPrimeiraTela()
  const falsa = usarApiFalsa()
  if (o.logado ?? true) falsa.logado()
  falsa.estado.atrasoRefresh = o.atrasoDoBoot ?? 0
  const { sessao, api } = novaSessao({ prazoDoConfirmMs: o.prazoDoConfirmMs })
  const cliente = criarClienteDeConsultas()
  // Nos testes, erro é erro: sem as tentativas que escondem um 500.
  cliente.setDefaultOptions({
    ...cliente.getDefaultOptions(),
    queries: { ...cliente.getDefaultOptions().queries, retry: false },
  })
  const router = createMemoryRouter(rotas, { initialEntries: [rota] })
  const usuario = userEvent.setup()
  const tela = render(
    <StrictMode>
      <ProvedorDoPainel sessao={sessao} api={api} cliente={cliente}>
        <ProvedorDeTema>
          <ProvedorDeAvisos>
            <RouterProvider router={router} />
          </ProvedorDeAvisos>
        </ProvedorDeTema>
      </ProvedorDoPainel>
    </StrictMode>,
  )
  const boot = sessao.iniciar()
  if (!o.atrasoDoBoot) await boot
  return { falsa, sessao, api, cliente, router, usuario, tela }
}

export { EMAIL, SENHA }
