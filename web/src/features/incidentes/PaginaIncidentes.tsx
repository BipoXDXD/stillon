import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { useIncidentes } from '@/lib/api/consultas'
import { naoEncontrado } from '@/lib/api/http'
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
    <li className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-borda bg-superficie p-4">
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
        {i.acknowledged_at && (
          // tabIndex -1: o foco vem para cá quando o Reconhecer some (I1).
          <p id={`ack-${i.id}`} tabIndex={-1} className="text-sm text-suave outline-none">
            Reconhecido em {dataHora(i.acknowledged_at)}
          </p>
        )}
      </div>
      {!i.resolved_at && !i.acknowledged_at && (
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          // aria-disabled, e não disabled: o botão desabilitado perderia o
          // foco para o <body> enquanto o POST voa.
          aria-disabled={reconhecendo || undefined}
          onClick={() => {
            if (!reconhecendo) aoReconhecer(i.id)
          }}
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
  const avisar = useAvisar()
  const [erro, setErro] = useState<string | null>(null)
  const [reconhecendo, setReconhecendo] = useState<ReadonlySet<string>>(new Set())
  // O incidente cujo Reconhecer acabou de ser usado. Se o botão sumiu (o ack
  // valeu), o foco iria ao <body>: vai ao "Reconhecido em" da linha, ou ao
  // título da tela se a linha também saiu da lista.
  // Um ref, e um efeito a cada render: o render que o pede é o de tirar o
  // incidente de "reconhecendo", já com a lista relida.
  const focarDepois = useRef<string | null>(null)
  useEffect(() => {
    const id = focarDepois.current
    if (!id) return
    focarDepois.current = null
    if (document.activeElement && document.activeElement !== document.body) return
    const destino = document.getElementById(`ack-${id}`) ?? document.querySelector<HTMLElement>('main h1')
    destino?.focus()
  })

  async function reconhecer(id: string) {
    setErro(null)
    setReconhecendo((s) => new Set(s).add(id))
    try {
      await api.reconhecer(id)
    } catch (e) {
      // M3: o incidente sumiu com o alvo, apagado em outra aba. A lista é
      // relida logo abaixo.
      if (naoEncontrado(e)) avisar('O incidente não existe mais: o alvo foi apagado, talvez em outra aba.')
      else setErro(mensagemDe(e))
    } finally {
      // Mesmo depois de uma falha: o ack pode ter chegado ao servidor.
      await qc.invalidateQueries({ queryKey: ['incidents'] })
      focarDepois.current = id
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
