import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { Esqueleto } from '@/components/EstadoDaConsulta'
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

function Linha({ i, agora, aoReconhecer }: { i: Incidente; agora: number; aoReconhecer: (id: string) => void }) {
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
          onClick={() => aoReconhecer(i.id)}
          aria-label={`Reconhecer o incidente de ${i.target_name}`}
          title="Marca que você está ciente"
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
  const itens = consulta.data?.pages.flatMap((pg) => pg.items) ?? []

  async function reconhecer(id: string) {
    setErro(null)
    try {
      await api.reconhecer(id)
      await qc.invalidateQueries({ queryKey: ['incidents'] })
    } catch (e) {
      setErro(mensagemDe(e))
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
            onClick={() => setParams({ status: f }, { replace: true })}
            className={cn('rounded-md border border-borda px-3 py-1 text-sm', f === filtro && 'bg-painel font-semibold')}
          >
            {t}
          </button>
        ))}
      </fieldset>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      {consulta.isPending ? (
        <Esqueleto linhas={2} />
      ) : consulta.error ? (
        <div role="alert" className="space-y-2">
          <p>{mensagemDe(consulta.error)}</p>
          <Botao tipo="secundario" tamanho="pequeno" onClick={() => void consulta.refetch()}>
            Tentar de novo
          </Botao>
        </div>
      ) : itens.length === 0 ? (
        <p className="text-suave">Nenhum incidente {filtro === 'open' ? 'aberto' : filtro === 'resolved' ? 'resolvido' : ''}.</p>
      ) : (
        <>
          <ul className="space-y-3">
            {itens.map((i) => (
              <Linha key={i.id} i={i} agora={consulta.dataUpdatedAt} aoReconhecer={(id) => void reconhecer(id)} />
            ))}
          </ul>
          {/* Botão, e não rolagem infinita: previsível no teclado e no leitor de tela. */}
          {consulta.hasNextPage && (
            <Botao tipo="secundario" disabled={consulta.isFetchingNextPage} onClick={() => void consulta.fetchNextPage()}>
              Carregar mais
            </Botao>
          )}
        </>
      )}
    </>
  )
}
