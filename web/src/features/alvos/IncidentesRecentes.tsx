import { Link } from 'react-router'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { periodoDoIncidente, SeloDoIncidente } from '@/components/SeloDoIncidente'
import type { useIncidentes } from '@/lib/api/consultas'
import { textoDaCausa } from '@/lib/api/mensagens'

// Os incidentes recentes do alvo (spec do redesign, Telas > 2): uma linha
// por incidente, com o selo da situação, a causa e o período. Pela
// EstadoDaConsulta (D17): um poll de fundo que falha mantém a lista.
export function IncidentesRecentes({ consulta, agora }: { consulta: ReturnType<typeof useIncidentes>; agora: Date }) {
  return (
    <section className="space-y-3" aria-labelledby="titulo-incidentes">
      <h2 id="titulo-incidentes" className="text-xl">
        Incidentes recentes
      </h2>
      <EstadoDaConsulta consulta={consulta}>
        {(dados) => {
          const itens = dados.pages.flatMap((p) => p.items)
          return itens.length === 0 ? (
            <p className="text-suave">Nenhum incidente.</p>
          ) : (
            <ul className="divide-y divide-borda rounded-padrao border border-borda bg-superficie px-4 sombra-nivel-1">
              {itens.map((i) => (
                <li key={i.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-3">
                  <SeloDoIncidente incidente={i} />
                  <span className="text-sm">{textoDaCausa(i.cause)}</span>
                  <span className="text-meta text-suave">{periodoDoIncidente(i, agora)}</span>
                </li>
              ))}
            </ul>
          )
        }}
      </EstadoDaConsulta>
      <Link to="/incidents" className="alvo-de-toque text-sm font-semibold text-primaria underline underline-offset-4">
        Todos os incidentes
      </Link>
    </section>
  )
}
