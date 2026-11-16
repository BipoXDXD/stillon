import type { UseInfiniteQueryResult } from '@tanstack/react-query'
import type { ReactNode, RefObject } from 'react'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { BotaoQueSome } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'

const ROTULOS = { eventos: 'Carregar mais', incidentes: 'Carregar mais incidentes' }

export function ListaPaginadaDoHeartbeat<T>({
  consulta,
  reserva,
  itens,
  children,
}: {
  consulta: UseInfiniteQueryResult<T>
  reserva: RefObject<HTMLHeadingElement | null>
  itens: keyof typeof ROTULOS
  children: (dados: T) => ReactNode
}) {
  // A falha de uma página só afeta o rodapé. Um 404 ainda é terminal.
  const erroNaPagina = consulta.isFetchNextPageError && !naoEncontrado(consulta.error)
  return (
    <EstadoDaConsulta consulta={erroNaPagina ? { ...consulta, error: null } : consulta}>
      {(dados) => (
        <>
          {children(dados)}
          {consulta.hasNextPage && (
            <div className="space-y-2">
              {erroNaPagina && (
                <ErroDoFormulario>Não foi possível carregar mais {itens}. {mensagemDe(consulta.error)}</ErroDoFormulario>
              )}
              <BotaoQueSome
                tipo="secundario"
                reserva={reserva}
                aria-disabled={consulta.isFetchingNextPage || undefined}
                onClick={() => {
                  if (!consulta.isFetchingNextPage) void consulta.fetchNextPage()
                }}
              >
                {erroNaPagina ? 'Tentar de novo' : ROTULOS[itens]}
              </BotaoQueSome>
            </div>
          )}
        </>
      )}
    </EstadoDaConsulta>
  )
}
