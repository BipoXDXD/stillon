import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { ListaDeTags } from '@/components/ListaDeTags'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { estiloDeCartao } from '@/components/ui/cartao'
import { Confirmar } from '@/components/ui/dialogo'
import { chaves, useHeartbeat } from '@/lib/api/consultas'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe, textoDaCausa } from '@/lib/api/mensagens'
import type { Heartbeat } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'
import { DialogoDaUrlDePing } from './DialogoDaUrlDePing'
import { comoEditavel } from './esquema'
import { EventosDoHeartbeat } from './EventosDoHeartbeat'
import { IncidentesDoHeartbeat } from './IncidentesDoHeartbeat'
import { estadoDoHeartbeat, inicioEmCurso, prazoVencido, tempoDeExecucao, textoDaDuracao, textoDaEspera, textoDoAgendamento, textoDoHeartbeat } from './estado'

// De um detalhe a outro a rota é a mesma: a key recomeça a tela, como a do alvo.
export function PaginaHeartbeat() {
  const { id = '' } = useParams()
  return <DetalheDoHeartbeat key={id} id={id} />
}

type Confirmacao = 'pausar' | 'apagar' | 'rotacionar'

function DetalheDoHeartbeat({ id }: { id: string }) {
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const consulta = useHeartbeat(id)
  const [confirmando, setConfirmando] = useState<Confirmacao | null>(null)
  const [ocupado, setOcupado] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  // Só aqui, no estado da tela (spec da tela, T8).
  const [urlNova, setUrlNova] = useState<string | null>(null)

  // Sai primeiro e só então mexe no cache: com a tela montada, invalidar
  // refaria o GET e o 404 piscaria (o mesmo cuidado do detalhe do alvo).
  async function sair(aviso: string) {
    avisar(aviso)
    await navegar('/', { flushSync: true })
    qc.removeQueries({ queryKey: chaves.heartbeat(id) })
    void qc.invalidateQueries({ queryKey: chaves.heartbeats })
  }

  const sumiu = () => sair('O heartbeat já tinha sido apagado, talvez em outra aba.')

  // O poll que volta 404 depois de o heartbeat estar na tela: ele sumiu. Um 404
  // na primeira carga fica na tela como "Não encontrado.".
  const sumiuNoPoll = naoEncontrado(consulta.error) && consulta.data !== undefined ? consulta.errorUpdatedAt : 0
  const tratado = useRef(0)
  const aoSumirNoPoll = useEffectEvent((quando: number) => {
    if (quando === tratado.current) return
    tratado.current = quando
    void sumiu()
  })
  useEffect(() => {
    if (sumiuNoPoll) aoSumirNoPoll(sumiuNoPoll)
  }, [sumiuNoPoll])

  // Uma ação de cada vez: o estado segura o clique duplo e o Confirmar. Ao
  // terminar, ela fecha só a própria confirmação: a ação espera a tela
  // atualizar, e nesse intervalo a pessoa pode ter aberto outra.
  async function executar(acao: () => Promise<void>, confirmacao: Confirmacao) {
    if (ocupado) return
    setOcupado(true)
    setErro(null)
    try {
      await acao()
    } catch (e) {
      if (naoEncontrado(e)) await sumiu()
      else setErro(mensagemDe(e))
    } finally {
      setOcupado(false)
      setConfirmando((aberta) => (aberta === confirmacao ? null : aberta))
    }
  }

  const alternar = (ativo: boolean) =>
    executar(async () => {
      await api.alterarHeartbeat(id, { enabled: ativo })
      await qc.invalidateQueries({ queryKey: chaves.heartbeats })
      avisar(ativo ? 'Heartbeat retomado.' : 'Heartbeat pausado.')
    }, 'pausar')

  const apagar = () =>
    executar(async () => {
      await api.apagarHeartbeat(id)
      await sair('Heartbeat apagado.')
    }, 'apagar')

  const rotacionar = () =>
    executar(async () => {
      const { ping_url: url } = await api.rotacionarHeartbeat(id)
      setUrlNova(url)
    }, 'rotacionar')

  return (
    <EstadoDaConsulta consulta={consulta}>
      {(hb) => (
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
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="min-w-0 space-y-2">
                <Titulo aba={hb.name}>{hb.name}</Titulo>
                <SeloDeEstado estado={estadoDoHeartbeat(hb.state)} texto={textoDoHeartbeat(hb.state)} />
                <ListaDeTags tags={hb.tags} />
              </div>
              <div className="flex flex-wrap gap-2">
                {/* Num agendamento de versão posterior, todo PATCH é 409
                    schedule_unsupported: editar, pausar e retomar ficam de fora.
                    Apagar e gerar nova URL seguem. */}
                {comoEditavel(hb) !== null && (
                  <>
                    <Botao tipo="secundario" asChild>
                      <Link to={`/heartbeats/${id}/edit`}>Editar</Link>
                    </Botao>
                    {/* Um botão só, que muda de texto: o foco devolvido pelo
                        Confirmar ("Pausar") cai no mesmo elemento, agora "Retomar". */}
                    <Botao
                      tipo="terciario"
                      aria-disabled={ocupado || undefined}
                      onClick={() => {
                        if (ocupado) return
                        if (hb.enabled) setConfirmando('pausar')
                        else void alternar(true)
                      }}
                    >
                      {hb.enabled ? 'Pausar' : 'Retomar'}
                    </Botao>
                  </>
                )}
                <Botao tipo="terciario" onClick={() => setConfirmando('apagar')}>
                  Apagar
                </Botao>
              </div>
            </div>
            {hb.description !== '' && <p className="max-w-prose break-words whitespace-pre-wrap">{hb.description}</p>}
          </div>
          <ErroDoFormulario>{erro}</ErroDoFormulario>
          <Sinais hb={hb} agora={new Date(consulta.dataUpdatedAt)} />
          <IncidentesDoHeartbeat id={id} agora={new Date(consulta.dataUpdatedAt)} aoErrar={setErro} aoSumir={() => void sumiu()} />
          <EventosDoHeartbeat id={id} />
          <UrlDePing aoGerar={() => setConfirmando('rotacionar')} />

          <Confirmar
            aberto={confirmando === 'pausar'}
            aoMudar={(v) => !v && setConfirmando(null)}
            titulo="Pausar o heartbeat?"
            descricao="Enquanto pausado, nenhum incidente abre e os lembretes param. Os sinais da tarefa continuam registrados."
            acao="Pausar heartbeat"
            ocupado={ocupado}
            aoConfirmar={() => void alternar(false)}
          />
          <Confirmar
            aberto={confirmando === 'apagar'}
            aoMudar={(v) => !v && setConfirmando(null)}
            titulo={`Apagar “${hb.name}”?`}
            descricao="O log de eventos e os incidentes deste heartbeat também são apagados. Não há como desfazer."
            acao="Apagar heartbeat"
            ocupado={ocupado}
            aoConfirmar={() => void apagar()}
          />
          <Confirmar
            aberto={confirmando === 'rotacionar'}
            aoMudar={(v) => !v && setConfirmando(null)}
            titulo="Gerar uma nova URL de ping?"
            descricao="A URL atual para de funcionar na hora. Até a tarefa usar a nova, os sinais dela são recusados."
            acao="Gerar nova URL"
            ocupado={ocupado}
            aoConfirmar={() => void rotacionar()}
          />
          <DialogoDaUrlDePing url={urlNova} aoFechar={() => setUrlNova(null)} />
        </div>
      )}
    </EstadoDaConsulta>
  )
}

function UrlDePing({ aoGerar }: { aoGerar: () => void }) {
  return (
    <section aria-labelledby="titulo-url" className={`${estiloDeCartao} space-y-3`}>
      <h2 id="titulo-url" className="text-xl">
        URL de ping
      </h2>
      <p className="text-sm text-suave">
        A URL só aparece quando é gerada. Perdeu a sua? Gere outra e troque na tarefa.
      </p>
      <Botao tipo="secundario" onClick={aoGerar}>
        Gerar nova URL
      </Botao>
    </section>
  )
}

function fraseDoEstado(hb: Heartbeat, agora: Date): string | null {
  if (hb.state === 'late') {
    const causa = textoDaCausa('heartbeat_late')
    return prazoVencido(hb, agora) ? `${causa}: o prazo era ${dataHora(hb.deadline_at)}.` : `${causa}. O incidente segue aberto até um sinal de sucesso.`
  }
  if (hb.state === 'failing') return `${textoDaCausa('heartbeat_failed')}.`
  if (hb.state !== 'waiting') return null
  // Num agendamento de versão posterior a data guardada não é prazo (Prazo).
  return hb.schedule.kind === 'unknown' ? `${textoDaEspera(hb)}.` : `${textoDaEspera(hb)}, até ${dataHora(hb.deadline_at)}.`
}

const quando = (iso: string | null) => (iso === null ? 'Nunca' : dataHora(iso))

const PRAZO_NAO_ACOMPANHADO = 'Prazo não acompanhado nesta versão'

// Pausado não tem prazo correndo, e prazo que passou não é "próximo".
// Num agendamento de versão posterior o servidor não move o prazo: a data
// guardada não diz quando a tarefa deveria sinalizar.
function Prazo({ hb, agora }: { hb: Heartbeat; agora: Date }) {
  if (hb.state === 'paused') return null
  if (hb.schedule.kind === 'unknown') return <Dado nome="Prazo" valor={PRAZO_NAO_ACOMPANHADO} />
  return <Dado nome={prazoVencido(hb, agora) ? 'Prazo vencido' : 'Próximo prazo'} valor={dataHora(hb.deadline_at)} />
}

function Sinais({ hb, agora }: { hb: Heartbeat; agora: Date }) {
  const frase = fraseDoEstado(hb, agora)
  const inicio = inicioEmCurso(hb)
  return (
    <section aria-labelledby="titulo-sinais" className={`${estiloDeCartao} space-y-4`}>
      <h2 id="titulo-sinais" className="text-xl">
        Agendamento e sinais
      </h2>
      {frase && <p>{frase}</p>}
      <dl className="grid gap-4 sm:grid-cols-2">
        <Dado nome="Agendamento" valor={textoDoAgendamento(hb.schedule)} />
        <Dado nome="Tolerância" valor={textoDaDuracao(hb.grace_sec)} />
        <Prazo hb={hb} agora={agora} />
        <Dado nome="Último sinal" valor={quando(hb.last_ping_at)} />
        <Dado nome="Último sucesso" valor={quando(hb.last_success_at)} />
        <Dado nome="Última duração" valor={hb.last_runtime_ms === null ? 'Não medida' : tempoDeExecucao(hb.last_runtime_ms)} />
        {inicio !== null && <Dado nome="Em execução desde" valor={dataHora(inicio)} />}
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
