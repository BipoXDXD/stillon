import { Activity, ArrowUpRight, Bell, Plus } from 'lucide-react'
import { useEffect, useRef } from 'react'
import { Link } from 'react-router'
import { useAnunciar } from '@/components/Anunciador'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Minigrafico } from '@/components/Minigrafico'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { useCanais, useResumo } from '@/lib/api/consultas'
import type { ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido, textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { dataHora, haQuanto, milissegundos, porcento } from '@/lib/format'
import { mapaDeEstados, transicoes } from './transicoes'

function resumoEmTexto(itens: ItemDoResumo[]): string {
  const conta = new Map<EstadoExibido, number>()
  for (const it of itens) {
    const e = estadoExibido(it.state, it.enabled)
    conta.set(e, (conta.get(e) ?? 0) + 1)
  }
  const partes: string[] = []
  const add = (e: EstadoExibido, um: string, varios: string) => {
    const n = conta.get(e) ?? 0
    if (n) partes.push(`${n} ${n === 1 ? um : varios}`)
  }
  add('up', 'no ar', 'no ar')
  add('degraded', 'degradado', 'degradados')
  add('down', 'fora do ar', 'fora do ar')
  add('unknown', 'sem dados', 'sem dados')
  add('pausado', 'pausado', 'pausados')
  return partes.join(', ')
}

function Cartao({ it, agora }: { it: ItemDoResumo; agora: Date }) {
  return (
    <li className="cartao-alvo min-w-0 space-y-5 rounded-lg border border-borda bg-superficie p-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Link to={`/targets/${it.id}`} className="alvo-de-toque min-w-0 break-words text-lg font-bold leading-snug tracking-tight underline-offset-4 hover:text-realce hover:underline">
          {it.name}
        </Link>
        <SeloDeEstado estado={estadoExibido(it.state, it.enabled)} />
      </div>
      <dl className="grid grid-cols-2 gap-x-3 gap-y-2 text-sm [&_dd]:text-right [&_dd]:font-medium [&_dd]:tabular-nums">
        <dt className="text-suave">Latência</dt>
        <dd>{milissegundos(it.last_latency_ms)}</dd>
        <dt className="text-suave">No ar em 24 h</dt>
        <dd>{it.uptime_24h === null ? '—' : porcento(it.uptime_24h)}</dd>
        <dt className="text-suave">Verificado</dt>
        <dd>{it.last_checked_at ? haQuanto(it.last_checked_at, agora) : 'nunca'}</dd>
      </dl>
      <div className="flex items-center justify-between gap-3 border-t border-borda pt-3">
        <span className="text-xs text-suave">Últimas 24 h</span>
        <Minigrafico horas={it.spark} agora={agora} />
      </div>
      {it.open_incident_id && (
        <Link to="/incidents?status=open" className="alvo-de-toque inline-flex items-center gap-1 text-sm font-medium text-fora underline underline-offset-4">
          Incidente aberto <ArrowUpRight aria-hidden="true" className="size-4" />
        </Link>
      )}
    </li>
  )
}

// A consulta de canais é independente: falhar nunca esconde os alvos nem
// transforma "não se sabe" em "nenhum canal".
function AvisoDeCanais() {
  const consulta = useCanais()
  const canais = consulta.data?.items
  let frases: string[] = []
  if (consulta.error) frases = ['Não foi possível conferir os canais de notificação agora.']
  else if (canais) {
    if (!canais.length) frases.push('Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.')
    else {
      const pausados = canais.filter((c) => !c.enabled).length
      if (pausados) frases.push(pausados === 1
        ? '1 canal pausado. Os avisos desse canal são descartados.'
        : `${pausados} canais pausados. Os avisos desses canais são descartados.`)
      if (!canais.some((c) => c.enabled && (c.status === 'verified' || c.status === 'failing'))) {
        frases.push('Nenhum canal verificado está ativo para receber avisos.')
      }
      const falhando = canais.filter((c) => c.enabled && c.status === 'failing').length
      if (falhando) frases.push(falhando === 1 ? '1 canal está falhando na entrega.' : `${falhando} canais estão falhando na entrega.`)
    }
  }
  if (!frases.length) return null
  return (
    <section aria-label="Avisos de notificação" className="flex flex-wrap items-start gap-3 rounded-md border border-borda bg-painel p-4 text-sm">
      <Bell aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-suave" />
      <div className="min-w-0 flex-1 space-y-1">{frases.map((frase) => <p key={frase}>{frase}</p>)}</div>
      <Link to="/channels" className="alvo-de-toque basis-full pl-7 font-medium text-realce underline underline-offset-4 sm:basis-auto sm:pl-0">Configurar canais</Link>
    </section>
  )
}

const ESTADOS: EstadoExibido[] = ['up', 'degraded', 'down', 'unknown', 'pausado']

function ResumoOperacional({ itens }: { itens: ItemDoResumo[] }) {
  const conta = new Map<EstadoExibido, number>()
  for (const it of itens) {
    const estado = estadoExibido(it.state, it.enabled)
    conta.set(estado, (conta.get(estado) ?? 0) + 1)
  }
  return (
    <section aria-label="Resumo dos alvos">
      <dl className="resumo-operacional grid grid-cols-2 gap-y-4 py-5 sm:grid-cols-3 xl:grid-cols-5">
        {ESTADOS.map((estado) => (
          <div key={estado} className="px-5 py-1">
            <dt className="text-sm text-suave">{textoDoEstado(estado)}</dt>
            <dd className="mt-2 text-4xl font-bold tracking-tight tabular-nums">{conta.get(estado) ?? 0}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

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

  const agora = new Date(consulta.dataUpdatedAt)
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="space-y-2">
          <Titulo aba="Painel">Painel</Titulo>
          <p className="text-sm text-suave">Acompanhe a disponibilidade dos seus serviços.</p>
        </div>
        <Botao asChild>
          <Link to="/targets/new"><Plus aria-hidden="true" className="size-4" />Cadastrar alvo</Link>
        </Botao>
      </div>
      <AvisoDeCanais />
      <EstadoDaConsulta consulta={consulta}>
        {(d) =>
          d.items.length === 0 ? (
            <p className="rounded-lg border border-dashed border-borda bg-superficie p-8 text-center">
              Nenhum alvo ainda.{' '}
              <Link to="/targets/new" className="text-realce underline">
                Cadastrar o primeiro alvo
              </Link>
            </p>
          ) : (
            <>
              <ResumoOperacional itens={d.items} />
              <div className="flex flex-wrap items-center justify-between gap-2">
                <h2 className="flex items-center gap-2 text-xl font-bold"><Activity aria-hidden="true" className="size-4 text-suave" />Seus alvos</h2>
                <p className="text-xs text-suave">Última atualização: <time dateTime={agora.toISOString()}>{dataHora(agora.toISOString())}</time></p>
              </div>
              <p className="sr-only">{resumoEmTexto(d.items)}</p>
              <ul className="grid gap-4 md:grid-cols-2 2xl:grid-cols-3">
                {d.items.map((it) => (
                  <Cartao key={it.id} it={it} agora={agora} />
                ))}
              </ul>
            </>
          )
        }
      </EstadoDaConsulta>
    </>
  )
}
