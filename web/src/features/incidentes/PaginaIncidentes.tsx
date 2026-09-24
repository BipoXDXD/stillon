import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { useIncidentes } from '@/lib/api/consultas'
import { mensagemDe, textoDoErroDeCheck } from '@/lib/api/mensagens'
import type { Incidente } from '@/lib/api/schemas'
import { dataHora, duracao } from '@/lib/format'
import { cn } from '@/lib/utils'

type Filtro = 'open' | 'resolved' | 'all'
const FILTROS: [Filtro, string][] = [
  ['open', 'Abertos'],
  ['resolved', 'Resolvidos'],
  ['all', 'Todos'],
]

// O texto visível que descreve o botão Reconhecer (aria-describedby): o ack
// é idempotente no servidor e não resolve nada, só marca que você viu.
const ID_AJUDA_ACK = 'ajuda-reconhecer'

function Linha({
  i,
  agora,
  reconhecendo,
  aoReconhecer,
}: {
  i: Incidente
  agora: number
  reconhecendo: boolean
  aoReconhecer: (id: string) => void
}) {
  const inicio = new Date(i.started_at).getTime()
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-borda p-4">
      <div className="space-y-1">
        <Link to={`/targets/${i.target_id}`} className="font-semibold underline-offset-2 hover:underline">
          {i.target_name}
        </Link>
        <p className="text-sm">
          {textoDoErroDeCheck(i.cause, null)} · início {dataHora(i.started_at)} ·{' '}
          {i.resolved_at
            ? `durou ${duracao(new Date(i.resolved_at).getTime() - inicio)}`
            : `em andamento há ${duracao(agora - inicio)}`}
        </p>
        {i.acknowledged_at && <p className="text-sm text-suave">Reconhecido em {dataHora(i.acknowledged_at)}</p>}
      </div>
      {!i.resolved_at && !i.acknowledged_at && (
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          disabled={reconhecendo}
          onClick={() => aoReconhecer(i.id)}
          aria-label={`Reconhecer o incidente de ${i.target_name}`}
          aria-describedby={ID_AJUDA_ACK}
        >
          Reconhecer
        </Botao>
      )}
    </li>
  )
}

export function PaginaIncidentes() {
  const [params, setParams] = useSearchParams()
  const p = params.get('status')
  const filtro: Filtro = p === 'resolved' || p === 'all' ? p : 'open'
  const consulta = useIncidentes(filtro)
  const api = useApi()
  const qc = useQueryClient()
  const [erro, setErro] = useState<string | null>(null)
  const [reconhecendo, setReconhecendo] = useState<ReadonlySet<string>>(new Set())

  async function reconhecer(id: string) {
    setErro(null)
    setReconhecendo((s) => new Set(s).add(id))
    try {
      await api.reconhecer(id)
    } catch (e) {
      setErro(mensagemDe(e))
    } finally {
      // Mesmo depois de uma falha: o ack pode ter chegado ao servidor.
      await qc.invalidateQueries({ queryKey: ['incidents'] })
      setReconhecendo((s) => {
        const n = new Set(s)
        n.delete(id)
        return n
      })
    }
  }

  return (
    <>
      <Titulo aba="Incidentes">Incidentes</Titulo>
      <fieldset className="flex gap-1">
        <legend className="sr-only">Filtro</legend>
        {FILTROS.map(([f, t]) => (
          <button
            key={f}
            type="button"
            aria-pressed={f === filtro}
            onClick={() => {
              setErro(null)
              setParams({ status: f }, { replace: true })
            }}
            className={cn('rounded-md border border-borda px-3 py-1 text-sm', f === filtro && 'bg-painel font-semibold')}
          >
            {t}
          </button>
        ))}
      </fieldset>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      {/* D17: um poll ou um "Carregar mais" que falha mantém a lista, com o
          aviso de dados velhos; só sem dados nenhuns o erro toma a tela. */}
      <EstadoDaConsulta consulta={consulta}>
        {(d) => {
          const itens = d.pages.flatMap((pg) => pg.items)
          if (itens.length === 0) {
            return (
              <p className="text-suave">
                Nenhum incidente{filtro === 'open' ? ' aberto' : filtro === 'resolved' ? ' resolvido' : ''}.
              </p>
            )
          }
          return (
            <>
              {itens.some((i) => !i.resolved_at && !i.acknowledged_at) && (
                <p id={ID_AJUDA_ACK} className="text-sm text-suave">
                  Reconhecer marca que você está ciente; não resolve o incidente.
                </p>
              )}
              <ul className="space-y-3">
                {itens.map((i) => (
                  <Linha
                    key={i.id}
                    i={i}
                    agora={consulta.dataUpdatedAt}
                    reconhecendo={reconhecendo.has(i.id)}
                    aoReconhecer={(id) => void reconhecer(id)}
                  />
                ))}
              </ul>
              {/* Botão, e não rolagem infinita: previsível no teclado e no leitor de tela. */}
              {consulta.hasNextPage && (
                <Botao
                  tipo="secundario"
                  disabled={consulta.isFetchingNextPage}
                  onClick={() => void consulta.fetchNextPage()}
                >
                  Carregar mais
                </Botao>
              )}
            </>
          )
        }}
      </EstadoDaConsulta>
    </>
  )
}
