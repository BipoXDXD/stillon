import { useRef } from 'react'
import { Navigate, Outlet, useLocation } from 'react-router'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { BotaoQueSome, TITULO_DA_TELA, useReservaDeFoco } from '@/components/ui/botao'
import { useSessao } from './contexto'

// O splash da verificação do boot e do chunk de uma rota sob demanda. É o
// <main> da vez (tabIndex -1): o "Tentar de novo" da porta, que some no
// clique, deixa o foco aqui, e daqui ele segue para a tela que vier depois
// (o painel, o login ou a própria porta, se a rede falhar de novo).
export function SplashDoPainel() {
  const eu = useRef<HTMLElement>(null)
  useReservaDeFoco(eu, TITULO_DA_TELA)
  return (
    <>
      <AvisoDeContexto />
      <main ref={eu} tabIndex={-1} className="grid min-h-screen place-items-center outline-none" aria-busy="true">
        <p className="text-suave">Carregando o StillOn…</p>
      </main>
    </>
  )
}

// A porta das rotas autenticadas. Enquanto o boot verifica o cookie, um
// splash — nunca o /login, que piscaria a cada F5 (spec §11.2, item 1).
export function ExigirSessao() {
  const { sessao, estado, motivo } = useSessao()
  const local = useLocation()

  if (estado === 'verificando') return <SplashDoPainel />
  if (estado === 'offline') {
    return (
      <>
        <AvisoDeContexto />
        <main className="mx-auto mt-16 max-w-md p-4">
          <div role="alert" className="space-y-4 text-center">
            {/* tabIndex -1: a reserva do foco quando o splash da nova tentativa some
                e a rede falhou de novo. */}
            <h1 tabIndex={-1} className="text-xl font-semibold outline-none">
              Não foi possível falar com o servidor
            </h1>
            <p className="text-suave">Sua sessão pode estar ativa; tente de novo em instantes.</p>
            <BotaoQueSome reserva={TITULO_DA_TELA} onClick={() => void sessao.tentarDeNovo()}>
              Tentar de novo
            </BotaoQueSome>
          </div>
        </main>
      </>
    )
  }
  if (estado === 'anonimo') {
    const next = local.pathname + local.search
    const q = next === '/' ? '' : `?next=${encodeURIComponent(next)}`
    return <Navigate to={`/login${q}`} replace state={{ motivo }} />
  }
  return <Outlet />
}
