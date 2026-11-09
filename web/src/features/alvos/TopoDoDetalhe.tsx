import { CircleHelp } from 'lucide-react'
import { useEffect, useId } from 'react'
import { useAnunciar } from '@/components/Anunciador'
import { BotaoQueSome, TITULO_DA_TELA } from '@/components/ui/botao'
import { useResumo, useSerie } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import type { Incidente } from '@/lib/api/schemas'
import { emMilissegundos, emPorcento, indicadorDaConsulta, textoDoAviso, ultimoCheck } from './estado-do-alvo'
import { FaixaDeEstado } from './FaixaDeEstado'
import { Indicadores, type LinhaDoIndicador } from './Indicadores'
import { resumir, type Janela } from './serie'

const JANELA_DO_DIA: Janela = '24h'
const JANELA_DO_MES: Janela = '30d'

// A janela do gráfico é uma destas consultas? Então o botão dela é o do aviso.
export function janelaCobertaPeloAviso(janela: Janela): boolean {
  return janela === JANELA_DO_DIA || janela === JANELA_DO_MES
}

type Props = {
  id: string
  incidentes: readonly Incidente[]
  aoSumir: () => void
  aoErrar: (mensagem: string | null) => void
}

// A faixa de estado e os KPIs, que dependem de três consultas: o resumo, a
// série de 24 h (a mesma da janela padrão do gráfico) e a horária de 30 dias
// (os dados por hora atrasam até ~1h15).
export function TopoDoDetalhe({ id, incidentes, aoSumir, aoErrar }: Props) {
  const resumo = useResumo()
  const dia = useSerie(id, JANELA_DO_DIA)
  const mes = useSerie(id, JANELA_DO_MES)
  const item = resumo.data?.items.find((x) => x.id === id)
  const agora = new Date(resumo.dataUpdatedAt)

  const falharam = [resumo, dia, mes].filter((c) => c.isError)
  const aviso = textoDoAviso(resumo, dia.isError || mes.isError)
  const carregando = resumo.isLoading || dia.isLoading || mes.isLoading

  const linhas: LinhaDoIndicador[] = [
    { rotulo: 'No ar em 24 h', valor: indicadorDaConsulta(resumo, () => emPorcento(item?.uptime_24h ?? null, 'Sem checks em 24 h')) },
    { rotulo: 'No ar em 30 dias', valor: indicadorDaConsulta(mes, (s) => emPorcento(resumir(s).uptime, 'Sem checks em 30 dias')) },
    { rotulo: 'Latência mediana (24 h)', valor: indicadorDaConsulta(dia, (s) => emMilissegundos(resumir(s).mediana, 'Sem checks em 24 h')) },
    { rotulo: 'Último check', valor: indicadorDaConsulta(resumo, () => ultimoCheck(item, agora)) },
  ]

  return (
    <>
      {resumo.isLoading && <EsqueletoDaFaixa />}
      {item && <FaixaDeEstado item={item} incidentes={incidentes} agora={agora} aoSumir={aoSumir} aoErrar={aoErrar} />}
      {aviso !== null && (
        <AvisoDasConsultas
          texto={aviso}
          causa={mensagemDe(falharam[0]?.error)}
          aoTentar={() => {
            for (const c of falharam) void c.refetch()
          }}
        />
      )}
      <div className="space-y-4" aria-busy={carregando || undefined}>
        {carregando && <p className="sr-only">Carregando os indicadores…</p>}
        <Indicadores linhas={linhas} />
      </div>
    </>
  )
}

function EsqueletoDaFaixa() {
  return (
    <div
      aria-hidden="true"
      data-testid="esqueleto-da-faixa"
      className="h-20 animate-pulse rounded-padrao bg-painel motion-reduce:animate-none sm:h-24"
    />
  )
}

// Neutro, no tom do estado "sem dados", e não vermelho: borda de perigo se
// leria como "o alvo caiu", e o que falhou foi a consulta. O botão fica na
// coluna do texto para o aviso não crescer no celular. Quem avisa o leitor de
// tela é o Anunciador: uma região viva que nasce preenchida muitas vezes não é lida.
function AvisoDasConsultas({ texto, causa, aoTentar }: { texto: string; causa: string; aoTentar: () => void }) {
  const idDoTitulo = useId()
  const anunciar = useAnunciar()
  useEffect(() => {
    anunciar(texto)
  }, [texto, anunciar])
  return (
    <section aria-labelledby={idDoTitulo} className="flex items-start gap-4 rounded-padrao bg-sem-dados-100 p-4 sm:p-6">
      <span className="inline-flex size-12 shrink-0 items-center justify-center rounded-full bg-superficie text-sem-dados">
        <CircleHelp aria-hidden="true" className="size-6" strokeWidth={2.5} />
      </span>
      <div className="min-w-0 flex-1 space-y-2">
        <h2 id={idDoTitulo} className="text-lg">
          {texto}
        </h2>
        <p className="text-sm text-suave">{causa}</p>
        <BotaoQueSome tipo="secundario" tamanho="pequeno" reserva={TITULO_DA_TELA} onClick={aoTentar}>
          Tentar de novo
        </BotaoQueSome>
      </div>
    </section>
  )
}
