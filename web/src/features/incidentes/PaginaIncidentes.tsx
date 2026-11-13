import { CircleCheck } from 'lucide-react'
import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { aoApontarPreCarregar, carregarDetalhe } from '@/app/rotas-sob-demanda'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import {
  periodoDoIncidente,
  SeloDoIncidente,
  situacaoDoIncidente,
  type SituacaoDoIncidente,
} from '@/components/SeloDoIncidente'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { estiloDeCartao } from '@/components/ui/cartao'
import { ControleSegmentado, type OpcaoSegmentada } from '@/components/ui/controle-segmentado'
import { estadoDoHeartbeat, textoDoHeartbeat } from '@/features/heartbeats/estado'
import { useTarefasQuePedemAtencao } from '@/features/heartbeats/tarefas-que-pedem-atencao'
import { chaves, useHeartbeats, useIncidentes, useIncidentesAbertos } from '@/lib/api/consultas'
import { textoDaCausa } from '@/lib/api/mensagens'
import type { Heartbeat, Incidente } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'
import { cn } from '@/lib/utils'
import { agruparIncidentes, destinoDepoisDeCarregar, resumoDosAbertos } from './agrupamento'
import { useReconhecer } from './reconhecer'

type Filtro = 'open' | 'resolved' | 'all'
const FILTROS: readonly OpcaoSegmentada<Filtro>[] = [
  { valor: 'open', rotulo: 'Abertos' },
  { valor: 'resolved', rotulo: 'Resolvidos' },
  { valor: 'all', rotulo: 'Todos' },
]

// O texto visível que descreve o botão Reconhecer (aria-describedby): o ack
// é idempotente no servidor e não resolve nada, só marca que você viu.
const ID_AJUDA_ACK = 'ajuda-reconhecer'

// Borda de acento (spec do redesign, Telas > 3): vermelha no aberto, índigo
// no reconhecido. O resolvido tem a mesma largura na cor da borda, sem
// acento visível, para o texto alinhar com o dos abertos.
const ACENTO: Record<SituacaoDoIncidente, string> = {
  'em-andamento': 'border-l-4 border-l-fora',
  reconhecido: 'border-l-4 border-l-primaria',
  resolvido: 'border-l-4 border-l-borda',
}

function Cartao({
  i,
  agora,
  reconhecendo,
  aoReconhecer,
}: {
  i: Incidente
  agora: Date
  reconhecendo: boolean
  aoReconhecer: (id: string) => void
}) {
  const situacao = situacaoDoIncidente(i)
  return (
    // sm:flex-nowrap: na tela larga, o texto encolhe (min-w-0) e o Reconhecer
    // fica na linha do nome; no celular, o botão desce para baixo do texto.
    <li
      className={cn(
        'flex flex-wrap items-start justify-between gap-4 sm:flex-nowrap rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6',
        ACENTO[situacao],
      )}
    >
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          {/* O nome é o título do cartão, como no painel: o leitor de tela
              navega pelos títulos (h1 da tela, h2 do grupo, h3 do cartão). */}
          <h3 className="min-w-0 text-lg">
            <Link
              // O foco vem para cá depois do "Carregar mais".
              id={`incidente-${i.id}`}
              to={`/targets/${i.target_id}`}
              {...aoApontarPreCarregar(carregarDetalhe)}
              className={cn('alvo-de-toque underline-offset-4 hover:underline', situacao === 'resolvido' && 'text-apagado')}
            >
              {i.target_name}
            </Link>
          </h3>
          <SeloDoIncidente incidente={i} />
        </div>
        <p className="text-sm">
          {textoDaCausa(i.cause)} · {periodoDoIncidente(i, agora)}
        </p>
        {i.acknowledged_at && (
          // tabIndex -1: o foco vem para cá quando o Reconhecer some.
          <p id={`ack-${i.id}`} tabIndex={-1} className="text-sm text-suave outline-none">
            Reconhecido em {dataHora(i.acknowledged_at)}
            {situacao === 'reconhecido' && ' · lembretes parados'}
          </p>
        )}
      </div>
      {situacao === 'em-andamento' && (
        <Botao
          tipo="secundario"
          // aria-disabled, e não disabled: o botão desabilitado perderia o
          // foco para o <body> enquanto o POST voa.
          aria-disabled={reconhecendo || undefined}
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

function Grupo({ titulo, children }: { titulo: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="space-y-3">
      <h2 id={id} className="rotulo-de-grupo font-sans text-apagado">
        {titulo}
      </h2>
      {children}
    </section>
  )
}

// O vazio dos abertos é o estado bom: check em círculo verde.
function Vazio({ filtro, soDosAlvos }: { filtro: Filtro; soDosAlvos: boolean }) {
  if (filtro === 'resolved') return <p className="text-suave">Nenhum incidente resolvido.</p>
  // Com tarefa fora, ou sem saber das tarefas, dizer "nenhum" mentiria.
  if (soDosAlvos) {
    return <p className="text-suave">{filtro === 'open' ? 'Nenhum alvo com incidente aberto.' : 'Nenhum alvo com incidente.'}</p>
  }
  if (filtro === 'all') return <p className="text-suave">Nenhum incidente.</p>
  return (
    <div className="flex flex-col items-center gap-3 rounded-padrao border border-borda bg-superficie p-8 text-center sombra-nivel-1">
      <span className="inline-flex size-12 items-center justify-center rounded-full bg-no-ar-100 text-no-ar">
        <CircleCheck aria-hidden="true" className="size-6" />
      </span>
      <p className="font-display text-xl font-semibold">Nenhum incidente aberto.</p>
      <p className="text-sm text-suave">Quando um alvo cair, o incidente aparece aqui.</p>
    </div>
  )
}

// Os incidentes de heartbeat têm rotas próprias: a
// lista aponta as tarefas fora, em vez de fingir que não existem.
function TarefasQuePedemAtencao({ tarefas }: { tarefas: readonly Heartbeat[] }) {
  if (tarefas.length === 0) return null
  return (
    <Grupo titulo="Tarefas que pedem atenção">
      <ul className="space-y-3">
        {tarefas.map((hb) => (
          <li key={hb.id} className={cn(estiloDeCartao, 'flex flex-wrap items-center gap-x-3 gap-y-2 border-l-4 border-l-fora')}>
            <Link to={`/heartbeats/${hb.id}`} className="alvo-de-toque min-w-0 font-semibold underline-offset-4 hover:underline">
              {hb.name}
            </Link>
            <SeloDeEstado estado={estadoDoHeartbeat(hb.state)} texto={textoDoHeartbeat(hb.state)} />
          </li>
        ))}
      </ul>
    </Grupo>
  )
}

export function PaginaIncidentes() {
  const [params, setParams] = useSearchParams()
  const p = params.get('status')
  const filtro: Filtro = p === 'resolved' || p === 'all' ? p : 'open'
  const consulta = useIncidentes(filtro)
  // A mesma consulta do contador da navegação: a frase de resumo vale em
  // qualquer filtro, sem requisição a mais.
  const abertos = useIncidentesAbertos()
  const heartbeats = useHeartbeats()
  const tarefas = useTarefasQuePedemAtencao()
  const api = useApi()
  const avisar = useAvisar()
  const [erro, setErro] = useState<string | null>(null)
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

  const { reconhecendo, reconhecer } = useReconhecer({
    enviar: api.reconhecer,
    consulta: chaves.todosOsIncidentes,
    aoNaoEncontrar: () => avisar('O incidente não existe mais: o alvo foi apagado, talvez em outra aba.'),
    aoErrar: setErro,
    aoTerminar: (id) => {
      focarDepois.current = id
    },
  })

  // O destino do foco depois do "Carregar mais". O efeito roda depois do
  // render que põe os itens novos na tela e não tira o foco de quem saiu do
  // botão enquanto a página chegava.
  const botaoCarregarMais = useRef<HTMLButtonElement>(null)
  const [focarNoIncidente, setFocarNoIncidente] = useState<string>()
  useEffect(() => {
    if (!focarNoIncidente) return
    const ativo = document.activeElement
    if (ativo && ativo !== document.body && ativo !== botaoCarregarMais.current) return
    document.getElementById(`incidente-${focarNoIncidente}`)?.focus()
  }, [focarNoIncidente])

  async function carregarMais(itens: readonly Incidente[]) {
    if (consulta.isFetchingNextPage) return
    const conhecidos = new Set(itens.map((i) => i.id))
    const r = await consulta.fetchNextPage()
    const todos = r.data?.pages.flatMap((pg) => pg.items) ?? []
    setFocarNoIncidente(destinoDepoisDeCarregar(todos, conhecidos, r.hasNextPage))
  }

  const frase = abertos.data ? resumoDosAbertos(abertos.data.itens, abertos.data.completa) : null
  const agora = new Date(consulta.dataUpdatedAt)
  const cartao = (i: Incidente) => (
    <Cartao
      key={i.id}
      i={i}
      agora={agora}
      reconhecendo={reconhecendo.has(i.id)}
      aoReconhecer={(id) => void reconhecer(id)}
    />
  )

  return (
    <div className="space-y-8">
      <div className="space-y-2">
        <Titulo aba="Incidentes">Incidentes</Titulo>
        {frase && <p className="text-lg text-suave">{frase}</p>}
      </div>
      <ControleSegmentado
        rotulo="Filtro de incidentes"
        opcoes={FILTROS}
        valor={filtro}
        aoMudar={(f) => {
          setErro(null)
          setParams({ status: f }, { replace: true })
        }}
      />
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      {filtro !== 'resolved' && <TarefasQuePedemAtencao tarefas={tarefas} />}
      {/* Um poll ou um "Carregar mais" que falha mantém a lista, com o
          aviso de dados velhos; só sem dados nenhuns o erro toma a tela. */}
      <EstadoDaConsulta consulta={consulta}>
        {(d) => {
          const itens = d.pages.flatMap((pg) => pg.items)
          if (itens.length === 0) return <Vazio filtro={filtro} soDosAlvos={tarefas.length > 0 || !heartbeats.isSuccess} />
          const grupos = agruparIncidentes(itens)
          return (
            <div className="space-y-8">
              {grupos.abertos.some((i) => i.acknowledged_at === null) && (
                <p id={ID_AJUDA_ACK} className="text-sm text-suave">
                  Reconhecer interrompe os lembretes; não resolve o incidente.
                </p>
              )}
              {grupos.abertos.length > 0 && (
                <Grupo titulo="Abertos">
                  <ul className="space-y-3">{grupos.abertos.map(cartao)}</ul>
                </Grupo>
              )}
              {grupos.resolvidos.length > 0 && (
                <Grupo titulo="Resolvidos recentes">
                  <ul className="space-y-3">{grupos.resolvidos.map(cartao)}</ul>
                </Grupo>
              )}
              {/* Botão, e não rolagem infinita: previsível no teclado e no leitor de tela. */}
              {consulta.hasNextPage && (
                <Botao
                  ref={botaoCarregarMais}
                  tipo="secundario"
                  // aria-disabled, e não disabled: o botão desabilitado perderia
                  // o foco para o <body> enquanto a página chega.
                  aria-disabled={consulta.isFetchingNextPage || undefined}
                  onClick={() => void carregarMais(itens)}
                >
                  Carregar mais
                </Botao>
              )}
            </div>
          )
        }}
      </EstadoDaConsulta>
    </div>
  )
}
