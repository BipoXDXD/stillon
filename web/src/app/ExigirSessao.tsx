import { Navigate, Outlet, useLocation } from 'react-router'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Botao } from '@/components/ui/botao'
import { useSessao } from './contexto'

// A porta das rotas autenticadas. Enquanto o boot verifica o cookie, um
// splash — nunca o /login, que piscaria a cada F5 (spec §11.2, item 1).
export function ExigirSessao() {
  const { sessao, estado, motivo } = useSessao()
  const local = useLocation()

  if (estado === 'verificando') {
    return (
      <>
        <AvisoDeContexto />
        <div className="grid min-h-screen place-items-center" aria-busy="true">
          <p className="text-suave">Carregando o Vigilume…</p>
        </div>
      </>
    )
  }
  if (estado === 'offline') {
    return (
      <>
        <AvisoDeContexto />
        <div role="alert" className="mx-auto mt-24 max-w-md space-y-4 p-4 text-center">
          <h1 className="text-xl font-semibold">Não foi possível falar com o servidor</h1>
          <p className="text-suave">Sua sessão pode estar ativa; tente de novo em instantes.</p>
          <Botao onClick={() => void sessao.tentarDeNovo()}>Tentar de novo</Botao>
        </div>
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
