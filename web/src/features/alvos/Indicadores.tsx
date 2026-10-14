import { useSerie } from '@/lib/api/consultas'
import type { ItemDoResumo } from '@/lib/api/schemas'
import { emMilissegundos, emPorcento, ultimoCheck, type ValorDoIndicador } from './estado-do-alvo'
import { resumir } from './serie'

// Os quatro KPIs do detalhe (spec do redesign, Telas > 2), em Inter 600 de
// 28 px com a unidade em 13 px. "No ar em 24 h" e "Último check" vêm do
// resumo; "No ar em 30 dias", da janela horária de 30 dias (os dados por
// hora atrasam até ~1h15); a mediana, da janela de 24 h, a mesma consulta
// da janela padrão do gráfico.
export function Indicadores({ alvoId, item, agora }: { alvoId: string; item: ItemDoResumo | undefined; agora: Date }) {
  const dia = useSerie(alvoId, '24h')
  const mes = useSerie(alvoId, '30d')
  return (
    <dl className="grid grid-cols-2 gap-4 lg:grid-cols-4">
      <Indicador rotulo="No ar em 24 h" {...emPorcento(item?.uptime_24h ?? null)} />
      <Indicador rotulo="No ar em 30 dias" {...emPorcento(mes.data ? resumir(mes.data).uptime : null)} />
      <Indicador rotulo="Latência mediana (24 h)" {...emMilissegundos(dia.data ? resumir(dia.data).mediana : null)} />
      <Indicador rotulo="Último check" {...ultimoCheck(item?.last_checked_at ?? null, agora)} />
    </dl>
  )
}

function Indicador({ rotulo, valor, unidade }: ValorDoIndicador & { rotulo: string }) {
  return (
    <div className="min-w-0 space-y-1 rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1">
      <dt className="text-meta text-suave">{rotulo}</dt>
      <dd>
        <span className="text-3xl font-semibold">{valor}</span>
        {unidade && (
          <>
            {' '}
            <span className="text-meta font-normal text-suave">{unidade}</span>
          </>
        )}
      </dd>
    </div>
  )
}
