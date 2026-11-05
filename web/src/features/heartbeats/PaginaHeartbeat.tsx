import { ArrowLeft } from 'lucide-react'
import { Link, useParams } from 'react-router'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { estiloDeCartao } from '@/components/ui/cartao'
import { useHeartbeat } from '@/lib/api/consultas'
import { textoDaCausa } from '@/lib/api/mensagens'
import type { Heartbeat } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'
import { estadoDoHeartbeat, textoDaDuracao, textoDoAgendamento, textoDoHeartbeat } from './estado'

// De um detalhe a outro a rota é a mesma: a key recomeça a tela, como a do alvo.
export function PaginaHeartbeat() {
  const { id = '' } = useParams()
  return <DetalheDoHeartbeat key={id} id={id} />
}

function DetalheDoHeartbeat({ id }: { id: string }) {
  const consulta = useHeartbeat(id)
  return <EstadoDaConsulta consulta={consulta}>{(hb) => <Conteudo hb={hb} />}</EstadoDaConsulta>
}

function Conteudo({ hb }: { hb: Heartbeat }) {
  return (
    <div className="space-y-8">
      <div className="space-y-4">
        <Link
          to="/"
          aria-label="Voltar ao painel"
          className="alvo-de-toque inline-flex items-center gap-1 text-sm font-semibold text-primaria underline-offset-4 hover:underline"
        >
          <ArrowLeft aria-hidden="true" className="size-4" />
          Painel
        </Link>
        <div className="min-w-0 space-y-2">
          <Titulo aba={hb.name}>{hb.name}</Titulo>
          <SeloDeEstado estado={estadoDoHeartbeat(hb.state)} texto={textoDoHeartbeat(hb.state)} />
        </div>
      </div>
      <Sinais hb={hb} />
    </div>
  )
}

function fraseDoEstado(hb: Heartbeat): string | null {
  if (hb.state === 'late') return `${textoDaCausa('heartbeat_late')}: o prazo era ${dataHora(hb.deadline_at)}.`
  if (hb.state === 'failing') return `${textoDaCausa('heartbeat_failed')}.`
  return hb.state === 'waiting' ? `Aguardando o primeiro sinal, até ${dataHora(hb.deadline_at)}.` : null
}

const quando = (iso: string | null) => (iso === null ? 'Nunca' : dataHora(iso))

function Sinais({ hb }: { hb: Heartbeat }) {
  const frase = fraseDoEstado(hb)
  return (
    <section aria-labelledby="titulo-sinais" className={`${estiloDeCartao} space-y-4`}>
      <h2 id="titulo-sinais" className="text-xl">
        Agendamento e sinais
      </h2>
      {frase && <p>{frase}</p>}
      <dl className="grid gap-4 sm:grid-cols-2">
        <Dado nome="Agendamento" valor={textoDoAgendamento(hb.schedule)} />
        <Dado nome="Tolerância" valor={textoDaDuracao(hb.grace_sec)} />
        <Dado nome="Próximo prazo" valor={dataHora(hb.deadline_at)} />
        <Dado nome="Último sinal" valor={quando(hb.last_ping_at)} />
        <Dado nome="Último sucesso" valor={quando(hb.last_success_at)} />
        <Dado nome="Último início" valor={quando(hb.last_start_at)} />
      </dl>
    </section>
  )
}

function Dado({ nome, valor }: { nome: string; valor: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-meta text-suave">{nome}</dt>
      <dd className="break-words">{valor}</dd>
    </div>
  )
}
