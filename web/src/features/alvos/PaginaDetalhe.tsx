import { useQueryClient } from '@tanstack/react-query'
import { ArrowLeft } from 'lucide-react'
import { useEffect, useEffectEvent, useId, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { ControleSegmentado, type OpcaoSegmentada } from '@/components/ui/controle-segmentado'
import { Confirmar } from '@/components/ui/dialogo'
import { useAlvo, useIncidentes, useResumo, useSerie } from '@/lib/api/consultas'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe, textoDoErroDeCheck } from '@/lib/api/mensagens'
import type { Serie } from '@/lib/api/schemas'
import { dataHora, fuso, milissegundos, porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { FaixaDeEstado } from './FaixaDeEstado'
import { GraficoDeLatencia } from './GraficoDeLatencia'
import { IncidentesRecentes } from './IncidentesRecentes'
import { Indicadores } from './Indicadores'
import { ehJanela, JANELAS, resumir, type Janela } from './serie'

const NOMES: Record<Janela, string> = { '1h': '1 h', '6h': '6 h', '24h': '24 h', '7d': '7 dias', '30d': '30 dias', '90d': '90 dias' }
const OPCOES_DE_JANELA: readonly OpcaoSegmentada<Janela>[] = (Object.keys(JANELAS) as Janela[]).map((j) => ({
  valor: j,
  rotulo: NOMES[j],
}))

function Resumo({ id, serie }: { id: string; serie: Serie }) {
  const r = resumir(serie)
  const horaria = serie.resolution === '1h'
  return (
    <p id={id}>
      {horaria ? 'Mediana das horas' : 'Mediana'} {milissegundos(r.mediana)}, {horaria ? 'pior p95 horário' : 'p95'}{' '}
      {milissegundos(r.p95)}, {r.uptime === null ? 'sem checks' : `${porcento(r.uptime)} no ar`} nesta janela.
    </p>
  )
}

// A tabela do gráfico, para leitor de tela e teclado; números à direita.
function Tabela({ serie }: { serie: Serie }) {
  if (serie.resolution === '1h') {
    const linhas = serie.items.slice(-200).reverse()
    return (
      <table className="w-full text-left text-sm">
        <caption className="sr-only">Até as 200 horas mais recentes da janela, da mais recente</caption>
        <thead className="text-meta text-apagado">
          <tr>
            <th scope="col" className="py-2 font-normal">Hora</th>
            <th scope="col" className="py-2 text-right font-normal">Checks</th>
            <th scope="col" className="py-2 text-right font-normal">No ar</th>
            <th scope="col" className="py-2 text-right font-normal">Mediana</th>
            <th scope="col" className="py-2 text-right font-normal">p95</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-borda">
          {linhas.map((h) => (
            <tr key={h.bucket}>
              <td className="py-2">{dataHora(h.bucket)}</td>
              <td className="py-2 text-right">{h.total}</td>
              <td className="py-2 text-right">{h.total ? porcento(h.ok / h.total) : '—'}</td>
              <td className="py-2 text-right">{milissegundos(h.p50_ms)}</td>
              <td className="py-2 text-right">{milissegundos(h.p95_ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    )
  }
  const linhas = serie.items.slice(-200).reverse()
  return (
    <table className="w-full text-left text-sm">
      <caption className="sr-only">Até os 200 checks mais recentes da janela, do mais recente</caption>
      <thead className="text-meta text-apagado">
        <tr>
          <th scope="col" className="py-2 font-normal">Quando</th>
          <th scope="col" className="py-2 font-normal">Estado</th>
          <th scope="col" className="py-2 text-right font-normal">Latência</th>
          <th scope="col" className="py-2 font-normal">Detalhe</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-borda">
        {linhas.map((c) => (
          <tr key={c.checked_at}>
            <td className="py-2">{dataHora(c.checked_at)}</td>
            <td className="py-2">
              <SeloDeEstado estado={c.status} />
            </td>
            <td className="py-2 text-right">{c.status === 'down' ? '—' : milissegundos(c.latency_ms)}</td>
            <td className="py-2">{textoDoErroDeCheck(c.error_kind, c.status_code) || (c.status_code ?? '')}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// De um detalhe a outro a rota é a mesma, e o React reaproveitaria a tela:
// o erro, o diálogo de apagar e o "ocupado" de um alvo seguiriam no outro, e
// o título (com o alvo no cache) não receberia o foco. A key recomeça tudo.
export function PaginaDetalhe() {
  const { id = '' } = useParams()
  return <DetalheDoAlvo key={id} id={id} />
}

function DetalheDoAlvo({ id }: { id: string }) {
  const [params, setParams] = useSearchParams()
  const janelaDaUrl = params.get('janela')
  const janela: Janela = ehJanela(janelaDaUrl) ? janelaDaUrl : '24h'
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const alvo = useAlvo(id)
  const resumo = useResumo()
  const serie = useSerie(id, janela)
  // Os incidentes recentes do alvo: a lista do fim da tela e o aberto da faixa.
  const incidentes = useIncidentes('all', id, 10)
  const [apagando, setApagando] = useState(false)
  const [ocupado, setOcupado] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const noResumo = resumo.data?.items.find((x) => x.id === id)
  const agora = new Date(resumo.dataUpdatedAt)
  const idDoResumo = useId()

  // Sai primeiro, e só então tira do cache o que era do alvo: com a tela
  // ainda montada, invalidar (ou remover) refaria o GET /targets/:id, e o 404
  // piscaria. O flushSync faz a saída valer já no fim do await (sem ele, o
  // Router a aplica numa transição, e a tela ainda estaria lá). Depois, o
  // resumo e os incidentes (o contador da navegação) se atualizam.
  async function sairDoAlvo(aviso: string) {
    avisar(aviso)
    await navegar('/', { flushSync: true })
    qc.removeQueries({ queryKey: ['targets', id] })
    void qc.invalidateQueries({ queryKey: ['targets'] })
    void qc.invalidateQueries({ queryKey: ['incidents'] })
  }

  // O alvo foi apagado em outra aba (M3, o padrão de Canais).
  const sumiu = () => sairDoAlvo('O alvo já tinha sido apagado, talvez em outra aba.')

  // O poll (60 s, foco da janela) que volta 404 depois de o alvo estar na
  // tela: ele sumiu. Um 404 já na primeira carga (um endereço que nunca
  // existiu) fica na tela como "Não encontrado.". A chave é o instante do
  // 404, e o ref faz cada 404 contar uma vez (o StrictMode roda o efeito de
  // montagem duas vezes).
  const sumiuNoPoll = naoEncontrado(alvo.error) && alvo.data !== undefined ? alvo.errorUpdatedAt : 0
  const tratado = useRef(0)
  const aoSumirNoPoll = useEffectEvent((quando: number) => {
    if (quando === tratado.current) return
    tratado.current = quando
    void sumiu()
  })
  useEffect(() => {
    if (sumiuNoPoll) aoSumirNoPoll(sumiuNoPoll)
  }, [sumiuNoPoll])

  async function alternar(ativo: boolean) {
    setErro(null)
    try {
      await api.alterarAlvo(id, { enabled: ativo })
      await qc.invalidateQueries({ queryKey: ['targets'] })
    } catch (e) {
      if (naoEncontrado(e)) await sumiu()
      else setErro(mensagemDe(e))
    }
  }

  async function apagar() {
    setOcupado(true)
    try {
      await api.apagarAlvo(id)
      await sairDoAlvo('Alvo apagado.')
    } catch (e) {
      if (naoEncontrado(e)) {
        await sumiu()
        return
      }
      setErro(mensagemDe(e))
      setApagando(false)
    } finally {
      setOcupado(false)
    }
  }

  return (
    <EstadoDaConsulta consulta={alvo}>
      {(a) => (
        // Um <div>, e não <section> filho direto do <main>: a regra legada
        // "main > section" do index.css daria moldura a cada bloco.
        <div className="space-y-8">
          <div className="space-y-4">
            {/* "Painel" visível, "Voltar ao painel" no nome acessível: não
                colide com a aba "Painel" da navegação. */}
            <Link
              to="/"
              aria-label="Voltar ao painel"
              className="alvo-de-toque inline-flex items-center gap-1 text-sm font-semibold text-primaria underline-offset-4 hover:underline"
            >
              <ArrowLeft aria-hidden="true" className="size-4" />
              Painel
            </Link>
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="min-w-0 space-y-1">
                <Titulo aba={a.name}>{a.name}</Titulo>
                <p className="text-meta break-all text-apagado">
                  {a.method} {a.url} · a cada {a.interval_sec} s
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Botao tipo="secundario" asChild>
                  <Link to={`/targets/${id}/edit`}>Editar</Link>
                </Botao>
                <Botao tipo="terciario" onClick={() => void alternar(!a.enabled)}>
                  {a.enabled ? 'Pausar' : 'Retomar'}
                </Botao>
                <Botao tipo="terciario" onClick={() => setApagando(true)}>
                  Apagar
                </Botao>
              </div>
            </div>
          </div>
          <ErroDoFormulario>{erro}</ErroDoFormulario>
          {noResumo && (
            <FaixaDeEstado
              item={noResumo}
              incidentes={incidentes.data?.pages.flatMap((p) => p.items) ?? []}
              agora={agora}
              aoSumir={() => void sumiu()}
              aoErrar={setErro}
            />
          )}
          <Indicadores alvoId={id} item={noResumo} agora={agora} />

          <section
            className="space-y-4 rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1 sm:p-6"
            aria-labelledby="titulo-latencia"
          >
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h2 id="titulo-latencia" className="text-xl">
                Latência
              </h2>
              <ControleSegmentado
                rotulo="Janela do gráfico"
                opcoes={OPCOES_DE_JANELA}
                valor={janela}
                aoMudar={(j) => setParams({ janela: j }, { replace: true })}
              />
            </div>
            <EstadoDaConsulta consulta={serie}>
              {(s) => (
                // Trocando de janela, os números da janela anterior ficam na
                // tela (keepPreviousData) até a nova chegar: marcados como velhos.
                <div className={cn('space-y-3', serie.isPlaceholderData && 'opacity-60')} aria-busy={serie.isPlaceholderData}>
                  {serie.isPlaceholderData && <p className="text-sm text-suave">Carregando…</p>}
                  <Resumo id={idDoResumo} serie={s} />
                  <p className="text-sm text-suave">
                    {s.resolution === '1h'
                      ? 'Resolução por hora — os dados horários podem atrasar até cerca de 1h15.'
                      : 'Resolução por check.'}{' '}
                    Horários em {fuso()}.
                  </p>
                  {s.items.length === 0 ? (
                    <p className="text-sm text-suave">Nenhum check nesta janela.</p>
                  ) : (
                    <>
                      <GraficoDeLatencia serie={s} descritoPor={idDoResumo} />
                      <details>
                        <summary className="alvo-de-toque cursor-pointer text-sm">Ver dados em tabela (até os 200 mais recentes)</summary>
                        <Tabela serie={s} />
                      </details>
                    </>
                  )}
                </div>
              )}
            </EstadoDaConsulta>
          </section>

          <IncidentesRecentes consulta={incidentes} agora={agora} />

          <Confirmar
            aberto={apagando}
            aoMudar={setApagando}
            titulo={`Apagar “${a.name}”?`}
            descricao="O histórico de checks e incidentes deste alvo também é apagado. Não há como desfazer."
            acao="Apagar alvo"
            ocupado={ocupado}
            aoConfirmar={() => void apagar()}
          />
        </div>
      )}
    </EstadoDaConsulta>
  )
}
