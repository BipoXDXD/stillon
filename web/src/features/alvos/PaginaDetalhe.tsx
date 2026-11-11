import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useEffectEvent, useId, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { useAnunciar } from '@/components/Anunciador'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { SeloDeEstado } from '@/components/SeloDeEstado'
import { ListaDeTags } from '@/components/ListaDeTags'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { ControleSegmentado, type OpcaoSegmentada } from '@/components/ui/controle-segmentado'
import { Confirmar } from '@/components/ui/dialogo'
import { chaves, useAlvo, useIncidentes, useSerie } from '@/lib/api/consultas'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe, textoDoErroDeCheck } from '@/lib/api/mensagens'
import type { Serie } from '@/lib/api/schemas'
import { ATRASO_DO_SINAL_MS, useAposUmTempo } from '@/lib/atraso'
import { dataHora, fuso, milissegundos, porcento } from '@/lib/format'
import { NavegacaoDaLista } from '@/features/painel/NavegacaoDaLista'
import { CertificadoDoAlvo } from './CertificadoDoAlvo'
import { GraficoDeLatencia } from './GraficoDeLatencia'
import { IncidentesRecentes } from './IncidentesRecentes'
import { ehJanela, JANELAS, resumir, type Janela } from './serie'
import { janelaCobertaPeloAviso, TopoDoDetalhe } from './TopoDoDetalhe'

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
  const serie = useSerie(id, janela)
  // Os incidentes recentes do alvo: a lista do fim da tela e o aberto da faixa.
  const incidentes = useIncidentes('all', id, 10)
  const [apagando, setApagando] = useState(false)
  const [ocupado, setOcupado] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const idDoResumo = useId()
  const anunciar = useAnunciar()

  // O keepPreviousData não diz de que janela são os números na tela: é a
  // última que chegou de verdade.
  const [janelaNaTela, setJanelaNaTela] = useState(janela)
  if (serie.isSuccess && !serie.isPlaceholderData && janelaNaTela !== janela) setJanelaNaTela(janela)
  const esperaLonga = useAposUmTempo(serie.isPlaceholderData, ATRASO_DO_SINAL_MS)
  const sinalDaTroca = esperaLonga ? `Ainda em ${NOMES[janelaNaTela]}, carregando ${NOMES[janela]}…` : ''
  useEffect(() => {
    if (sinalDaTroca) anunciar(sinalDaTroca)
  }, [sinalDaTroca, anunciar])

  // Sai primeiro, e só então tira do cache o que era do alvo: com a tela
  // ainda montada, invalidar (ou remover) refaria o GET /targets/:id, e o 404
  // piscaria. O flushSync faz a saída valer já no fim do await (sem ele, o
  // Router a aplica numa transição, e a tela ainda estaria lá). Depois, o
  // resumo e os incidentes (o contador da navegação) se atualizam.
  async function sairDoAlvo(aviso: string) {
    avisar(aviso)
    await navegar('/', { flushSync: true })
    qc.removeQueries({ queryKey: chaves.alvo(id) })
    void qc.invalidateQueries({ queryKey: chaves.todosOsAlvos })
    void qc.invalidateQueries({ queryKey: chaves.todosOsIncidentes })
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
      await qc.invalidateQueries({ queryKey: chaves.todosOsAlvos })
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
        <div className="space-y-8">
          <div className="space-y-4">
            <NavegacaoDaLista chave={`http:${id}`} />
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="min-w-0 space-y-1">
                <Titulo aba={a.name}>{a.name}</Titulo>
                <p className="text-meta break-all text-apagado">
                  {a.method} {a.url} · a cada {a.interval_sec} s
                </p>
                <ListaDeTags tags={a.tags} />
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
          <TopoDoDetalhe
            id={id}
            incidentes={incidentes.data?.pages.flatMap((p) => p.items) ?? []}
            aoSumir={() => void sumiu()}
            aoErrar={setErro}
          />
          <CertificadoDoAlvo alvo={a} agora={new Date(alvo.dataUpdatedAt)} />

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
                // Só a janela muda: os filtros e a ordem da lista seguem na URL, para
                // o Anterior/Próximo.
                aoMudar={(j) =>
                  setParams(
                    (atual) => {
                      const nova = new URLSearchParams(atual)
                      nova.set('janela', j)
                      return nova
                    },
                    { replace: true },
                  )
                }
              />
            </div>
            {/* Depois do seletor e com a linha reservada: nada se mexe logo
                após o toque. Fora do aria-busy, que silenciaria o texto. */}
            <p data-testid="troca-de-janela" className="min-h-5 text-sm text-suave">
              {sinalDaTroca}
            </p>
            <EstadoDaConsulta consulta={serie} refeitaNoAviso={janelaCobertaPeloAviso(janela)}>
              {(s) => (
                // Trocando de janela, os números da janela anterior ficam na
                // tela (keepPreviousData) até a nova chegar. Opacidade no bloco
                // reprovaria o contraste do texto.
                <div className="space-y-3" aria-busy={serie.isPlaceholderData}>
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

          {/* O relógio da própria lista: o do resumo é 0 enquanto ele não responde. */}
          <IncidentesRecentes consulta={incidentes} agora={new Date(incidentes.dataUpdatedAt)} />

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
