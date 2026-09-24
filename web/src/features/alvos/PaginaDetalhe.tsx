import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta, Esqueleto } from '@/components/EstadoDaConsulta'
import { estadoExibido, SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { Confirmar } from '@/components/ui/dialogo'
import { useAlvo, useIncidentes, useResumo, useSerie } from '@/lib/api/consultas'
import { mensagemDe, textoDoErroDeCheck } from '@/lib/api/mensagens'
import type { Serie } from '@/lib/api/schemas'
import { dataHora, duracao, fuso, milissegundos, porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { GraficoDeLatencia } from './GraficoDeLatencia'
import { desde, ehJanela, JANELAS, resumir, type Janela } from './serie'

const NOMES: Record<Janela, string> = { '1h': '1 h', '6h': '6 h', '24h': '24 h', '7d': '7 dias', '30d': '30 dias', '90d': '90 dias' }

function Resumo({ serie }: { serie: Serie }) {
  const r = resumir(serie)
  const horaria = serie.resolution === '1h'
  return (
    <p>
      {horaria ? 'Mediana das horas' : 'Mediana'} {milissegundos(r.mediana)}, {horaria ? 'pior p95 horário' : 'p95'}{' '}
      {milissegundos(r.p95)}, {r.uptime === null ? 'sem checks' : `${porcento(r.uptime)} no ar`} nesta janela.
    </p>
  )
}

function Tabela({ serie }: { serie: Serie }) {
  if (serie.resolution === '1h') {
    const linhas = serie.items.slice(-200).reverse()
    return (
      <table className="w-full text-left text-sm">
        <caption className="sr-only">Horas da janela</caption>
        <thead>
          <tr>
            <th scope="col">Hora</th>
            <th scope="col">Checks</th>
            <th scope="col">No ar</th>
            <th scope="col">Mediana</th>
            <th scope="col">p95</th>
          </tr>
        </thead>
        <tbody>
          {linhas.map((h) => (
            <tr key={h.bucket}>
              <td>{dataHora(h.bucket)}</td>
              <td>{h.total}</td>
              <td>{h.total ? porcento(h.ok / h.total) : '—'}</td>
              <td>{milissegundos(h.p50_ms)}</td>
              <td>{milissegundos(h.p95_ms)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    )
  }
  const linhas = serie.items.slice(-200).reverse()
  return (
    <table className="w-full text-left text-sm">
      <caption className="sr-only">Checks da janela, do mais recente</caption>
      <thead>
        <tr>
          <th scope="col">Quando</th>
          <th scope="col">Estado</th>
          <th scope="col">Latência</th>
          <th scope="col">Detalhe</th>
        </tr>
      </thead>
      <tbody>
        {linhas.map((c) => (
          <tr key={c.checked_at}>
            <td>{dataHora(c.checked_at)}</td>
            <td>
              <SeloDeEstado estado={c.status} />
            </td>
            <td>{c.status === 'down' ? '—' : milissegundos(c.latency_ms)}</td>
            <td>{textoDoErroDeCheck(c.error_kind, c.status_code) || (c.status_code ?? '')}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Incidentes({ id }: { id: string }) {
  const consulta = useIncidentes('all', id, 10)
  const itens = consulta.data?.pages.flatMap((p) => p.items) ?? []
  return (
    <section className="space-y-2" aria-labelledby="titulo-incidentes">
      <h2 id="titulo-incidentes" className="text-lg font-semibold">
        Incidentes recentes
      </h2>
      {consulta.isPending ? (
        <Esqueleto linhas={2} />
      ) : consulta.error ? (
        <p role="alert">{mensagemDe(consulta.error)}</p>
      ) : itens.length === 0 ? (
        <p className="text-suave">Nenhum incidente.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {itens.map((i) => (
            <li key={i.id}>
              {dataHora(i.started_at)} —{' '}
              {i.resolved_at
                ? `durou ${duracao(new Date(i.resolved_at).getTime() - new Date(i.started_at).getTime())}`
                : 'em andamento'}{' '}
              ({textoDoErroDeCheck(i.cause, null)})
            </li>
          ))}
        </ul>
      )}
      <Link to="/incidents" className="text-sm text-realce underline">
        Todos os incidentes
      </Link>
    </section>
  )
}

export function PaginaDetalhe() {
  const { id = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const janela: Janela = ehJanela(params.get('janela')) ? (params.get('janela') as Janela) : '24h'
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const alvo = useAlvo(id)
  const resumo = useResumo()
  const serie = useSerie(id, janela, () => desde(janela, Date.now()))
  const [apagando, setApagando] = useState(false)
  const [ocupado, setOcupado] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const noResumo = resumo.data?.items.find((x) => x.id === id)

  async function alternar(ativo: boolean) {
    setErro(null)
    try {
      await api.alterarAlvo(id, { enabled: ativo })
      await qc.invalidateQueries({ queryKey: ['targets'] })
    } catch (e) {
      setErro(mensagemDe(e))
    }
  }

  async function apagar() {
    setOcupado(true)
    try {
      await api.apagarAlvo(id)
      await qc.invalidateQueries({ queryKey: ['targets'] })
      avisar('Alvo apagado.')
      navegar('/')
    } catch (e) {
      setErro(mensagemDe(e))
      setApagando(false)
    } finally {
      setOcupado(false)
    }
  }

  return (
    <EstadoDaConsulta consulta={alvo}>
      {(a) => (
        <>
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="space-y-1">
              <Titulo aba={a.name}>{a.name}</Titulo>
              <p className="text-sm break-all text-suave">
                {a.method} {a.url} · a cada {a.interval_sec} s
              </p>
              {noResumo && <SeloDeEstado estado={estadoExibido(noResumo.state, noResumo.enabled)} />}
            </div>
            <div className="flex flex-wrap gap-2">
              <Botao tipo="secundario" asChild>
                <Link to={`/targets/${id}/edit`}>Editar</Link>
              </Botao>
              <Botao tipo="secundario" onClick={() => void alternar(!a.enabled)}>
                {a.enabled ? 'Pausar' : 'Retomar'}
              </Botao>
              <Botao tipo="perigo" onClick={() => setApagando(true)}>
                Apagar
              </Botao>
            </div>
          </div>
          <ErroDoFormulario>{erro}</ErroDoFormulario>

          <section className="space-y-3" aria-labelledby="titulo-latencia">
            <h2 id="titulo-latencia" className="text-lg font-semibold">
              Latência
            </h2>
            <fieldset className="flex flex-wrap gap-1">
              <legend className="sr-only">Janela</legend>
              {(Object.keys(JANELAS) as Janela[]).map((j) => (
                <button
                  key={j}
                  type="button"
                  aria-pressed={j === janela}
                  onClick={() => setParams({ janela: j }, { replace: true })}
                  className={cn('rounded-md border border-borda px-3 py-1 text-sm', j === janela && 'bg-painel font-semibold')}
                >
                  {NOMES[j]}
                </button>
              ))}
            </fieldset>
            <EstadoDaConsulta consulta={serie}>
              {(s) => (
                <div className="space-y-3">
                  <Resumo serie={s} />
                  <p className="text-sm text-suave">
                    {s.resolution === '1h'
                      ? 'Resolução por hora — os dados horários podem atrasar até cerca de 1h15.'
                      : 'Resolução por check.'}{' '}
                    Horários em {fuso()}.
                  </p>
                  <GraficoDeLatencia serie={s} />
                  <details>
                    <summary className="cursor-pointer text-sm">Ver dados em tabela</summary>
                    <Tabela serie={s} />
                  </details>
                </div>
              )}
            </EstadoDaConsulta>
          </section>

          <Incidentes id={id} />

          <Confirmar
            aberto={apagando}
            aoMudar={setApagando}
            titulo={`Apagar “${a.name}”?`}
            descricao="O histórico de checks e incidentes deste alvo também é apagado. Não há como desfazer."
            acao="Apagar alvo"
            ocupado={ocupado}
            aoConfirmar={() => void apagar()}
          />
        </>
      )}
    </EstadoDaConsulta>
  )
}
