import { onlineManager } from '@tanstack/react-query'
import { isRouteErrorResponse, useRevalidator, useRouteError } from 'react-router'
import { Botao, BotaoQueSome, TITULO_DA_TELA } from '@/components/ui/botao'

// Com rede, uma falha de import() dinâmico quase sempre é "houve deploy e o
// chunk antigo sumiu": o handler Go responde 404 para asset ausente (e não o
// index), e aqui o painel oferece recarregar em vez de ficar em branco. Sem
// rede, recarregar levaria à página de erro do navegador: o revalidate repete
// só o import da rota; quando ele dá certo, esta tela some com o botão, e o
// foco vai ao título da tela que chegou.
export function ErroDeRota() {
  const erro = useRouteError()
  const { revalidate } = useRevalidator()
  const chunk =
    erro instanceof TypeError && /dynamically imported module|Importing a module script failed/i.test(erro.message)
  const semRede = chunk && !onlineManager.isOnline()
  const naoAchou = isRouteErrorResponse(erro) && erro.status === 404
  return (
    <div role="alert" className="mx-auto mt-16 max-w-md space-y-4 p-4 text-center">
      <h1 className="text-xl font-semibold">
        {semRede
          ? 'Esta tela depende da conexão'
          : chunk
            ? 'Há uma versão nova do painel'
            : naoAchou
              ? 'Página não encontrada'
              : 'Algo deu errado'}
      </h1>
      {semRede ? (
        <BotaoQueSome reserva={TITULO_DA_TELA} onClick={() => void revalidate()}>
          Tentar de novo
        </BotaoQueSome>
      ) : (
        <Botao onClick={() => window.location.reload()}>Recarregar</Botao>
      )}
    </div>
  )
}
