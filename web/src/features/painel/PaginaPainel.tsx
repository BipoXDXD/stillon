import { Bell, Check, Plus } from 'lucide-react'
import { useEffect, useRef, type RefObject } from 'react'
import { Link, useSearchParams } from 'react-router'
import { useAnunciar } from '@/components/Anunciador'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { Botao, BotaoQueSome } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { useCanais, useHeartbeats, useIncidentesAbertos, useResolvidosRecentes, useResumo, useResumoFiltrado } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import type { Heartbeat, ItemDoResumo } from '@/lib/api/schemas'
import { cn } from '@/lib/utils'
import { avisosDosCanais } from './avisos-dos-canais'
import { BarraDeFiltros } from './BarraDeFiltros'
import { EsqueletoDoPainel } from './EsqueletoDoPainel'
import {
  casaComBusca,
  contarDoConjunto,
  descreverFiltros,
  escreverFiltros,
  heartbeatNosFiltros,
  lerFiltros,
  SEM_FILTROS,
  temFiltroDeApi,
  type Filtros,
} from './filtros'
import { ordenarPorGravidade } from './grupos'
import { linhaDoAlvo, linhaDoHeartbeat } from './linhas'
import { detalheDaManchete, fraseDoUltimoResolvido, manchete, metaDoPainel, textoDaManchete, type Tarefas } from './manchete'
import { TabelaDeMonitores } from './TabelaDeMonitores'
import { anunciaveis, mapaDeEstados, transicoes } from './transicoes'

export function PaginaPainel() {
  const consulta = useResumo()
  const heartbeats = useHeartbeats()
  const anunciar = useAnunciar()
  const anterior = useRef<ReturnType<typeof mapaDeEstados> | null>(null)
  const itens = consulta.data?.items
  const dasTarefas = heartbeats.data?.items

  useEffect(() => {
    if (!itens) return
    const atuais = anunciaveis(itens, dasTarefas ?? [])
    const frases = transicoes(anterior.current, atuais)
    if (frases.length) anunciar(frases.join('. '))
    anterior.current = mapaDeEstados(atuais)
  }, [itens, dasTarefas, anunciar])

  const tarefas: Tarefas = { itens: dasTarefas ?? [], confirmadas: !heartbeats.isError }

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <Titulo aba="Painel">Painel</Titulo>
        <div className="flex flex-wrap gap-2">
          <Botao tipo="secundario" asChild>
            <Link to="/heartbeats/new">Cadastrar heartbeat</Link>
          </Botao>
          <Botao asChild>
            <Link to="/targets/new">
              <Plus aria-hidden="true" className="size-4" />
              Cadastrar alvo
            </Link>
          </Botao>
        </div>
      </div>
      <AvisoDeCanais />
      <EstadoDaConsulta consulta={consulta} esqueleto={<EsqueletoDoPainel />}>
        {(d) =>
          // Até a primeira resposta das tarefas, o esqueleto: a manchete não
          // fala do que ainda não sabe.
          heartbeats.isPending ? (
            <EsqueletoDoPainel />
          ) : (
            <>
              {heartbeats.isError && (
                <AvisoDasTarefas erro={heartbeats.error} aoTentar={() => void heartbeats.refetch()} />
              )}
              <Monitores itens={d.items} tarefas={tarefas} atualizadoEm={new Date(consulta.dataUpdatedAt)} />
            </>
          )
        }
      </EstadoDaConsulta>
    </>
  )
}

// Sem a resposta das tarefas, o aviso fica à vista e o painel segue com o que
// sabe. Dados velhos continuam na tabela (spec da tela, A manchete).
function AvisoDasTarefas({ erro, aoTentar }: { erro: unknown; aoTentar: () => void }) {
  return (
    <output className="flex flex-wrap items-center gap-3 rounded-padrao border border-perigo p-3 text-sm">
      <span>Não foi possível conferir as tarefas agora ({mensagemDe(erro)}).</span>
      <Botao tipo="secundario" tamanho="pequeno" onClick={aoTentar}>
        Tentar de novo
      </Botao>
    </output>
  )
}

type PropsDosMonitores = { itens: ItemDoResumo[]; tarefas: Tarefas; atualizadoEm: Date }

function Monitores({ itens, tarefas, atualizadoEm }: PropsDosMonitores) {
  return (
    <div className="space-y-6">
      {/* A manchete fala do conjunto inteiro: um filtro nunca esconde um incêndio. */}
      <Manchete itens={itens} tarefas={tarefas} atualizadoEm={atualizadoEm} />
      {itens.length + tarefas.itens.length > 0 && <MonitoresFiltrados itens={itens} tarefas={tarefas} atualizadoEm={atualizadoEm} />}
    </div>
  )
}

function MonitoresFiltrados({ itens, tarefas, atualizadoEm }: PropsDosMonitores) {
  const [parametros, setParametros] = useSearchParams()
  const busca = useRef<HTMLInputElement>(null)
  const filtros = lerFiltros(parametros)
  const filtraNaApi = temFiltroDeApi(filtros)
  const filtrada = useResumoFiltrado(filtros, filtraNaApi, { items: itens })
  const aoMudar = (f: Filtros) => setParametros(escreverFiltros(f), { replace: true })

  const esperando = filtraNaApi && filtrada.isPlaceholderData
  const base = filtraNaApi ? filtrada.data?.items : itens
  const visiveis = base?.filter((it) => casaComBusca(it, filtros.busca))
  const tarefasVisiveis = tarefas.itens.filter((hb) => heartbeatNosFiltros(hb, filtros))
  const total = itens.length + tarefas.itens.length
  // O relógio da tabela é o da resposta que ela mostra. Enquanto o placeholder
  // está na tela, ela ainda mostra a consulta completa.
  const agora = filtraNaApi && !esperando ? new Date(filtrada.dataUpdatedAt) : atualizadoEm
  const resultado = (() => {
    if (esperando) return 'Filtrando…'
    return visiveis ? `${visiveis.length + tarefasVisiveis.length} de ${total} monitores` : null
  })()
  const tabela = (lista: ItemDoResumo[]) => (
    <ResultadoDosFiltros
      itens={lista}
      heartbeats={tarefasVisiveis}
      filtros={filtros}
      atualizadoEm={agora}
      reserva={busca}
      aoMudar={aoMudar}
    />
  )
  return (
    <>
      <BarraDeFiltros
        itens={itens}
        contagem={contarDoConjunto(itens, tarefas.itens)}
        filtros={filtros}
        resultado={resultado}
        semResultado={!esperando && visiveis?.length === 0 && tarefasVisiveis.length === 0}
        busca={busca}
        aoMudar={aoMudar}
      />
      <div aria-busy={esperando}>
        {filtraNaApi ? <EstadoDaConsulta consulta={filtrada}>{() => tabela(visiveis ?? [])}</EstadoDaConsulta> : tabela(visiveis ?? [])}
      </div>
    </>
  )
}

function ResultadoDosFiltros({
  itens,
  heartbeats,
  filtros,
  atualizadoEm,
  reserva,
  aoMudar,
}: {
  itens: ItemDoResumo[]
  heartbeats: readonly Heartbeat[]
  filtros: Filtros
  atualizadoEm: Date
  reserva: RefObject<HTMLElement | null>
  aoMudar: (f: Filtros) => void
}) {
  // A mesma consulta do contador da navegação: nenhuma requisição a mais.
  const abertos = useIncidentesAbertos()
  if (itens.length + heartbeats.length === 0) {
    return (
      <div className={cn(estiloDeCartao, 'space-y-2')}>
        <p className="font-semibold">Nenhum monitor com esses filtros.</p>
        <p className="text-sm text-suave">{descreverFiltros(filtros)}</p>
        <BotaoQueSome tipo="terciario" tamanho="pequeno" reserva={reserva} onClick={() => aoMudar(SEM_FILTROS)}>
          Limpar filtros
        </BotaoQueSome>
      </div>
    )
  }
  const porId = new Map((abertos.data?.itens ?? []).map((i) => [i.id, i]))
  const linhas = ordenarPorGravidade([
    ...itens.map((it) => linhaDoAlvo(it, it.open_incident_id ? porId.get(it.open_incident_id) : undefined, atualizadoEm)),
    ...heartbeats.map((hb) => linhaDoHeartbeat(hb, atualizadoEm)),
  ])
  return <TabelaDeMonitores linhas={linhas} agora={atualizadoEm} />
}

// A frase de estado no topo (spec do redesign, Telas > 1), em Fraunces.
function Manchete({ itens, tarefas, atualizadoEm }: PropsDosMonitores) {
  const m = manchete(itens, tarefas)
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
  const detalhe = m.tipo === 'tudo-no-ar' ? fraseDoUltimoResolvido(resolvidos.data?.items) : detalheDaManchete(itens, tarefas.itens)
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
      <p className="text-meta text-apagado">{metaDoPainel(itens, tarefas.itens, atualizadoEm)}</p>
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
