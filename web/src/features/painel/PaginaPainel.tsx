import { ArrowRight, Bell, Check, Plus } from 'lucide-react'
import { useEffect, useId, useRef, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useAnunciar } from '@/components/Anunciador'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Minigrafico } from '@/components/Minigrafico'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { useCanais, useIncidentesAbertos, useResolvidosRecentes, useResumo } from '@/lib/api/consultas'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { estadoExibido } from '@/lib/estado'
import { milissegundos, porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { avisosDosCanais } from './avisos-dos-canais'
import { EsqueletoDoPainel } from './EsqueletoDoPainel'
import { agruparPorGravidade, detalheDoProblema, linhaSemDados } from './grupos'
import { fraseDoUltimoResolvido, manchete, metaDoPainel, resumoEmTexto, textoDaManchete } from './manchete'
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

// Um <div>, e não <section> filho direto do <main>: a regra legada
// "main > section" do index.css daria moldura de cartão a cada grupo.
function Alvos({ itens, atualizadoEm }: { itens: ItemDoResumo[]; atualizadoEm: Date }) {
  const grupos = agruparPorGravidade(itens)
  // A mesma consulta do contador da navegação: nenhuma requisição a mais.
  const abertos = useIncidentesAbertos()
  const porId = new Map((abertos.data?.itens ?? []).map((i) => [i.id, i]))
  return (
    <div className="space-y-8">
      <Manchete itens={itens} atualizadoEm={atualizadoEm} />
      {grupos.problemas.length > 0 && (
        <Grupo titulo="Pedem atenção">
          <ul className="grid gap-4 md:grid-cols-2">
            {grupos.problemas.map((it) => (
              <CartaoDeProblema
                key={it.id}
                it={it}
                incidente={it.open_incident_id ? porId.get(it.open_incident_id) : undefined}
                agora={atualizadoEm}
              />
            ))}
          </ul>
        </Grupo>
      )}
      {grupos.funcionando.length > 0 && (
        <Grupo titulo="Funcionando">
          <TabelaFuncionando itens={grupos.funcionando} agora={atualizadoEm} />
        </Grupo>
      )}
      {grupos.semDadosOuPausados.length > 0 && (
        <Grupo titulo="Sem dados ou pausados">
          <ul className="divide-y divide-borda">
            {grupos.semDadosOuPausados.map((it) => (
              <li key={it.id} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-3">
                <Link to={`/targets/${it.id}`} className="alvo-de-toque font-semibold underline-offset-4 hover:underline">
                  {it.name}
                </Link>
                <span className="text-sm text-suave">{linhaSemDados(it, atualizadoEm)}</span>
              </li>
            ))}
          </ul>
        </Grupo>
      )}
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

// O cartão inteiro é clicável (auditoria P8): o ::before do link do nome
// cobre o cartão. "Ver incidente" fica por cima dele (z-10) e leva a outro
// lugar; "Ver detalhes" é só a pista visual do clique no cartão, e não um
// segundo link para o mesmo destino.
function CartaoDeProblema({ it, incidente, agora }: { it: ItemDoResumo; incidente: Incidente | undefined; agora: Date }) {
  const estado = estadoExibido(it.state, it.enabled)
  return (
    <li
      className={cn(
        'relative min-w-0 space-y-3 rounded-padrao border border-l-4 border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6',
        estado === 'down' ? 'border-l-fora' : 'border-l-degradado',
      )}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <h3 className="min-w-0 text-xl">
          <Link
            to={`/targets/${it.id}`}
            className="underline-offset-4 before:absolute before:inset-0 before:rounded-padrao hover:underline"
          >
            {it.name}
          </Link>
        </h3>
        <SeloDeEstado estado={estado} />
      </div>
      <p className="text-sm text-suave">{detalheDoProblema(it, incidente, agora)}</p>
      <p className="text-sm">
        No ar em 24 h: <span className="font-semibold">{it.uptime_24h === null ? '—' : porcento(it.uptime_24h)}</span>
      </p>
      {it.open_incident_id ? (
        <Link
          to="/incidents?status=open"
          aria-label={`Ver incidente de ${it.name}`}
          className="alvo-de-toque z-10 inline-flex items-center gap-1 text-sm font-semibold text-primaria underline underline-offset-4"
        >
          Ver incidente <ArrowRight aria-hidden="true" className="size-4" />
        </Link>
      ) : (
        <span aria-hidden="true" className="inline-flex items-center gap-1 text-sm font-semibold text-primaria">
          Ver detalhes <ArrowRight className="size-4" />
        </span>
      )}
    </li>
  )
}

// Linhas compactas (spec: 30 alvos sem rolar várias telas), com o cabeçalho
// de colunas uma vez só. Cada linha tem 44 px; números à direita.
function TabelaFuncionando({ itens, agora }: { itens: ItemDoResumo[]; agora: Date }) {
  return (
    <div className="rounded-padrao border border-borda bg-superficie sombra-nivel-1">
      <table className="w-full text-sm">
        <caption className="sr-only">Alvos no ar: latência do último check, tempo no ar em 24 h e as últimas 24 horas</caption>
        <thead>
          <tr className="text-meta text-apagado">
            <th scope="col" className="px-2 py-2 text-left font-normal sm:px-4">Alvo</th>
            <th scope="col" className="px-2 py-2 text-right font-normal sm:px-4">Latência</th>
            <th scope="col" className="px-2 py-2 text-right font-normal sm:px-4">24 h</th>
            <th scope="col" className="px-2 py-2 text-left font-normal sm:px-4">Últimas 24 h</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-borda border-t border-borda">
          {itens.map((it) => (
            <tr key={it.id} className="h-11">
              <th scope="row" className="px-2 text-left font-normal sm:px-4">
                <Link to={`/targets/${it.id}`} className="alvo-de-toque font-semibold underline-offset-4 hover:underline">
                  {it.name}
                </Link>
              </th>
              <td className="px-2 text-right sm:px-4">{milissegundos(it.last_latency_ms)}</td>
              <td className="px-2 text-right sm:px-4">{it.uptime_24h === null ? '—' : porcento(it.uptime_24h)}</td>
              <td className="px-2 sm:px-4">
                <Minigrafico horas={it.spark} agora={agora} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
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
