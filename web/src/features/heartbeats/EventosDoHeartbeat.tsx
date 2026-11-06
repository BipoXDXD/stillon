import { Check, Play, X, type LucideIcon } from 'lucide-react'
import { useRef } from 'react'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Selo, type TomDoSelo } from '@/components/SeloDeEstado'
import { BotaoQueSome } from '@/components/ui/botao'
import { useEventosDoHeartbeat } from '@/lib/api/consultas'
import type { EventoDeHeartbeat } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'
import { tempoDeExecucao } from './estado'

// Forma + cor + texto, como os outros selos.
const TIPOS: Record<EventoDeHeartbeat['kind'], { texto: string; tom: TomDoSelo; Icone: LucideIcon }> = {
  start: { texto: 'Início', tom: 'primaria', Icone: Play },
  success: { texto: 'Sucesso', tom: 'no-ar', Icone: Check },
  fail: { texto: 'Falha', tom: 'fora', Icone: X },
}

export function EventosDoHeartbeat({ id }: { id: string }) {
  const consulta = useEventosDoHeartbeat(id)
  const titulo = useRef<HTMLHeadingElement>(null)
  return (
    <section className="space-y-3" aria-labelledby="titulo-eventos">
      {/* tabIndex -1: o foco vem para cá quando o "Carregar mais" some. */}
      <h2 ref={titulo} id="titulo-eventos" tabIndex={-1} className="text-xl outline-none">
        Log de eventos
      </h2>
      <EstadoDaConsulta consulta={consulta}>
        {(dados) => {
          const itens = dados.pages.flatMap((p) => p.items)
          if (itens.length === 0) return <p className="text-suave">Nenhum sinal recebido.</p>
          return (
            <>
              <ul className="divide-y divide-borda rounded-padrao border border-borda bg-superficie px-4 sombra-nivel-1">
                {itens.map((e) => (
                  <Evento key={e.id} evento={e} />
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
                  Carregar mais
                </BotaoQueSome>
              )}
            </>
          )
        }}
      </EstadoDaConsulta>
    </section>
  )
}

function Evento({ evento }: { evento: EventoDeHeartbeat }) {
  const { texto, tom, Icone } = TIPOS[evento.kind]
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 py-3">
      <Selo tom={tom} Icone={Icone}>
        {texto}
      </Selo>
      <span className="text-sm">{dataHora(evento.at)}</span>
      {evento.runtime_ms !== null && <span className="text-meta text-suave">levou {tempoDeExecucao(evento.runtime_ms)}</span>}
      {evento.exit_code !== null && <span className="text-meta text-suave">código de saída {evento.exit_code}</span>}
    </li>
  )
}
