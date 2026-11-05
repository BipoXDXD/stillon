import { Bell, Check, Plus } from 'lucide-react'
import { useEffect, useRef } from 'react'
import { Link } from 'react-router'
import { useAnunciar } from '@/components/Anunciador'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { useCanais, useIncidentesAbertos, useResolvidosRecentes, useResumo } from '@/lib/api/consultas'
import type { ItemDoResumo } from '@/lib/api/schemas'
import { avisosDosCanais } from './avisos-dos-canais'
import { EsqueletoDoPainel } from './EsqueletoDoPainel'
import { ordenarPorGravidade } from './grupos'
import { linhaDoAlvo } from './linhas'
import { fraseDoUltimoResolvido, manchete, metaDoPainel, resumoEmTexto, textoDaManchete } from './manchete'
import { TabelaDeMonitores } from './TabelaDeMonitores'
import { mapaDeEstados, transicoes } from './transicoes'

export function PaginaPainel() {
  const consulta = useResumo()
  const anunciar = useAnunciar()
  const anterior = useRef<ReturnType<typeof mapaDeEstados> | null>(null)
  const itens = consulta.data?.items

  useEffect(() => {
    if (!itens) return
    const frases = transicoes(anterior.current, itens)
    if (frases.length) anunciar(frases.join('. '))
    anterior.current = mapaDeEstados(itens)
  }, [itens, anunciar])

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <Titulo aba="Painel">Painel</Titulo>
        <Botao asChild>
          <Link to="/targets/new">
            <Plus aria-hidden="true" className="size-4" />
            Cadastrar alvo
          </Link>
        </Botao>
      </div>
      <AvisoDeCanais />
      <EstadoDaConsulta consulta={consulta} esqueleto={<EsqueletoDoPainel />}>
        {(d) => <Alvos itens={d.items} atualizadoEm={new Date(consulta.dataUpdatedAt)} />}
      </EstadoDaConsulta>
    </>
  )
}

function Alvos({ itens, atualizadoEm }: { itens: ItemDoResumo[]; atualizadoEm: Date }) {
  // A mesma consulta do contador da navegação: nenhuma requisição a mais.
  const abertos = useIncidentesAbertos()
  const porId = new Map((abertos.data?.itens ?? []).map((i) => [i.id, i]))
  const linhas = ordenarPorGravidade(itens).map((it) =>
    linhaDoAlvo(it, it.open_incident_id ? porId.get(it.open_incident_id) : undefined, atualizadoEm),
  )
  return (
    <div className="space-y-6">
      <Manchete itens={itens} atualizadoEm={atualizadoEm} />
      {linhas.length > 0 && <TabelaDeMonitores linhas={linhas} agora={atualizadoEm} />}
    </div>
  )
}

// A frase de estado no topo (spec do redesign, Telas > 1), em Fraunces.
function Manchete({ itens, atualizadoEm }: { itens: ItemDoResumo[]; atualizadoEm: Date }) {
  const m = manchete(itens)
  const resolvidos = useResolvidosRecentes(m.tipo === 'tudo-no-ar')
  if (m.tipo === 'sem-alvos') {
    return (
      <div className="space-y-2">
        <h2 className="text-2xl sm:text-3xl">{textoDaManchete(m)}</h2>
        <p className="text-suave">
          Cadastre um endereço para o StillOn vigiar.{' '}
          <Link to="/targets/new" className="alvo-de-toque font-semibold text-primaria underline underline-offset-4">
            Cadastrar o primeiro alvo
          </Link>
        </p>
      </div>
    )
  }
  const detalhe = m.tipo === 'tudo-no-ar' ? fraseDoUltimoResolvido(resolvidos.data?.items) : `${resumoEmTexto(itens)}.`
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-3">
        {m.tipo === 'tudo-no-ar' && (
          <span className="inline-flex size-10 shrink-0 items-center justify-center rounded-full bg-no-ar-100 text-no-ar">
            <Check aria-hidden="true" className="size-6" strokeWidth={2.5} />
          </span>
        )}
        <h2 className="text-2xl sm:text-3xl">{textoDaManchete(m)}</h2>
      </div>
      {detalhe && <p className="text-suave">{detalhe}</p>}
      <p className="text-meta text-apagado">{metaDoPainel(itens, atualizadoEm)}</p>
    </div>
  )
}

// A consulta de canais é independente: falhar nunca esconde os alvos nem
// transforma "não se sabe" em "nenhum canal" (avisos-dos-canais.ts).
function AvisoDeCanais() {
  const frases = avisosDosCanais(useCanais())
  if (!frases.length) return null
  return (
    <section aria-label="Avisos de notificação" className="flex items-start gap-3 rounded-padrao border border-borda bg-painel p-4 text-sm">
      <Bell aria-hidden="true" className="mt-1 size-4 shrink-0 text-suave" />
      <div className="min-w-0 flex-1 space-y-2">
        {frases.map((frase) => (
          <p key={frase}>{frase}</p>
        ))}
        <Link to="/channels" className="alvo-de-toque inline-block font-semibold text-primaria underline underline-offset-4">
          Configurar canais
        </Link>
      </div>
    </section>
  )
}
