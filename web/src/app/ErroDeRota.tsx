import { isRouteErrorResponse, useRouteError } from 'react-router'
import { Botao } from '@/components/ui/botao'

// Uma falha de import() dinâmico quase sempre é "houve deploy e o chunk
// antigo sumiu": o handler Go responde 404 para asset ausente (e não o
// index), e aqui o painel oferece recarregar em vez de ficar em branco.
export function ErroDeRota() {
  const erro = useRouteError()
  const chunk =
    erro instanceof TypeError && /dynamically imported module|Importing a module script failed/i.test(erro.message)
  const naoAchou = isRouteErrorResponse(erro) && erro.status === 404
  return (
    <div role="alert" className="mx-auto mt-24 max-w-md space-y-4 p-4 text-center">
      <h1 className="text-xl font-semibold">
        {chunk ? 'Há uma versão nova do painel' : naoAchou ? 'Página não encontrada' : 'Algo deu errado'}
      </h1>
      <Botao onClick={() => window.location.reload()}>Recarregar</Botao>
    </div>
  )
}
