import { useEffect, useRef } from 'react'
import { Link } from 'react-router'
import { useAnunciar } from '@/components/Anunciador'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Minigrafico } from '@/components/Minigrafico'
import { estadoExibido, SeloDeEstado, type EstadoExibido } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { useResumo } from '@/lib/api/consultas'
import type { ItemDoResumo } from '@/lib/api/schemas'
import { haQuanto, milissegundos, porcento } from '@/lib/format'
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
    <li className="space-y-2 rounded-lg border border-borda p-4">
      <div className="flex items-start justify-between gap-2">
        <Link to={`/targets/${it.id}`} className="font-semibold underline-offset-2 hover:underline">
          {it.name}
        </Link>
        <SeloDeEstado estado={estadoExibido(it.state, it.enabled)} />
      </div>
      <dl className="grid grid-cols-2 gap-x-2 text-sm">
        <dt className="text-suave">Latência</dt>
        <dd>{milissegundos(it.last_latency_ms)}</dd>
        <dt className="text-suave">No ar em 24 h</dt>
        <dd>{it.uptime_24h === null ? '—' : porcento(it.uptime_24h)}</dd>
        <dt className="text-suave">Verificado</dt>
        <dd>{it.last_checked_at ? haQuanto(it.last_checked_at, agora) : 'nunca'}</dd>
      </dl>
      <Minigrafico horas={it.spark} agora={agora} />
      {it.open_incident_id && (
        <Link to="/incidents?status=open" className="block text-sm text-fora underline">
          Incidente aberto
        </Link>
      )}
    </li>
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
        <Titulo aba="Painel">Painel</Titulo>
        <Botao asChild>
          <Link to="/targets/new">Cadastrar alvo</Link>
        </Botao>
      </div>
      <EstadoDaConsulta consulta={consulta}>
        {(d) =>
          d.items.length === 0 ? (
            <p className="rounded-lg border border-borda p-6 text-center">
              Nenhum alvo ainda.{' '}
              <Link to="/targets/new" className="text-realce underline">
                Cadastrar o primeiro alvo
              </Link>
            </p>
          ) : (
            <>
              <p className="text-suave">{resumoEmTexto(d.items)}</p>
              <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
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
