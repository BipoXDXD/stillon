import { useRef } from 'react'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { periodoDoIncidente, SeloDoIncidente, situacaoDoIncidente } from '@/components/SeloDoIncidente'
import { BotaoQueSome } from '@/components/ui/botao'
import { useReconhecer } from '@/features/incidentes/reconhecer'
import { chaves, useIncidentesDoHeartbeat } from '@/lib/api/consultas'
import { textoDaCausa } from '@/lib/api/mensagens'
import type { IncidenteDeHeartbeat } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'

const ID_AJUDA = 'ajuda-reconhecer-heartbeat'

// O histórico do heartbeat, pela rota própria: /incidents não o traz.
export function IncidentesDoHeartbeat({
  id,
  agora,
  aoErrar,
  aoSumir,
}: {
  id: string
  agora: Date
  aoErrar: (mensagem: string | null) => void
  aoSumir: () => void
}) {
  const api = useApi()
  const consulta = useIncidentesDoHeartbeat(id)
  const titulo = useRef<HTMLHeadingElement>(null)

  const { reconhecendo, reconhecer } = useReconhecer({
    enviar: (incidente) => api.reconhecerDoHeartbeat(id, incidente),
    consulta: chaves.incidentesDoHeartbeat(id),
    aoNaoEncontrar: aoSumir,
    aoErrar,
  })

  return (
    <section className="space-y-3" aria-labelledby="titulo-incidentes-heartbeat">
      {/* tabIndex -1: o foco vem para cá quando o Reconhecer ou o "Carregar mais" somem. */}
      <h2 ref={titulo} id="titulo-incidentes-heartbeat" tabIndex={-1} className="text-xl outline-none">
        Incidentes
      </h2>
      <EstadoDaConsulta consulta={consulta}>
        {(dados) => {
          const itens = dados.pages.flatMap((p) => p.items)
          if (itens.length === 0) return <p className="text-suave">Nenhum incidente.</p>
          return (
            <>
              {itens.some((i) => situacaoDoIncidente(i) === 'em-andamento') && (
                <p id={ID_AJUDA} className="text-sm text-suave">
                  Reconhecer interrompe os lembretes; não resolve o incidente.
                </p>
              )}
              <ul className="divide-y divide-borda rounded-padrao border border-borda bg-superficie px-4 sombra-nivel-1">
                {itens.map((i) => (
                  <li key={i.id} className="flex flex-wrap items-center justify-between gap-3 py-3">
                    <Descricao incidente={i} agora={agora} />
                    {situacaoDoIncidente(i) === 'em-andamento' && (
                      <BotaoQueSome
                        tipo="secundario"
                        tamanho="pequeno"
                        reserva={titulo}
                        aria-disabled={reconhecendo.has(i.id) || undefined}
                        aria-describedby={ID_AJUDA}
                        aria-label={`Reconhecer o incidente de ${dataHora(i.started_at)}`}
                        onClick={() => void reconhecer(i.id)}
                      >
                        Reconhecer
                      </BotaoQueSome>
                    )}
                  </li>
                ))}
              </ul>
              {consulta.hasNextPage && (
                <BotaoQueSome
                  tipo="secundario"
                  reserva={titulo}
                  aria-disabled={consulta.isFetchingNextPage || undefined}
                  onClick={() => {
                    if (!consulta.isFetchingNextPage) void consulta.fetchNextPage()
                  }}
                >
                  Carregar mais incidentes
                </BotaoQueSome>
              )}
            </>
          )
        }}
      </EstadoDaConsulta>
    </section>
  )
}

function Descricao({ incidente: i, agora }: { incidente: IncidenteDeHeartbeat; agora: Date }) {
  const situacao = situacaoDoIncidente(i)
  return (
    <div className="min-w-0 space-y-1">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <SeloDoIncidente incidente={i} />
        <span className="text-sm">{textoDaCausa(i.cause)}</span>
      </div>
      <p className="text-meta text-suave">{periodoDoIncidente(i, agora)}</p>
      {i.acknowledged_at && (
        <p className="text-meta text-suave">
          Reconhecido em {dataHora(i.acknowledged_at)}
          {situacao === 'reconhecido' && ' · lembretes parados'}
        </p>
      )}
    </div>
  )
}
