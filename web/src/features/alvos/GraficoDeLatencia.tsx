import {
  Area,
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Scatter,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { Serie } from '@/lib/api/schemas'
import { pontosBrutos, pontosHorarios, type PontoBruto, type PontoHorario } from './serie'

// Recharts, só aqui: este arquivo entra no chunk do detalhe, carregado sob
// demanda. As cores são variáveis CSS do index.css, e não um <style> gerado
// em runtime como o ChartStyle do shadcn — que a CSP bloquearia sem nonce e
// faria a linha sumir (spike do Plano 6, §8.1).
const hora = new Intl.DateTimeFormat('pt-BR', { hour: '2-digit', minute: '2-digit' })
const dia = new Intl.DateTimeFormat('pt-BR', { day: '2-digit', month: '2-digit' })
const pct = (v: number) => `${Math.floor(v * 100)}%`

// descritoPor: o id do resumo em texto, que descreve o gráfico.
export function GraficoDeLatencia({ serie, descritoPor }: { serie: Serie; descritoPor?: string }) {
  const horaria = serie.resolution === '1h'
  const dados: (PontoBruto | PontoHorario)[] =
    serie.resolution === '1h' ? pontosHorarios(serie.items) : pontosBrutos(serie.items)
  const fmt = (t: number) => (horaria ? dia.format(t) : hora.format(t))
  return (
    // O nome e a descrição ficam no <svg> do Recharts, que é o que recebe o
    // foco (a accessibilityLayer: role="application", tabIndex 0). No
    // <figure> eles davam o nome em dobro e sumiam no foco do teclado.
    <figure className="h-64 w-full" data-testid="grafico-latencia">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart
          data={dados}
          margin={{ top: 8, right: 8, bottom: 0, left: 0 }}
          aria-label="Gráfico de latência"
          aria-describedby={descritoPor}
        >
          <CartesianGrid stroke="var(--borda)" vertical={false} />
          <XAxis
            dataKey="t"
            type="number"
            scale="time"
            domain={['dataMin', 'dataMax']}
            tickFormatter={fmt}
            stroke="var(--suave)"
          />
          <YAxis stroke="var(--suave)" unit=" ms" width={72} />
          {horaria && (
            // O uptime de cada hora (ok/total) em barras, no eixo da direita
            // (spec do Plano 6 §4.4). Atrás das linhas: vem antes delas.
            <YAxis
              yAxisId="uptime"
              orientation="right"
              domain={[0, 1]}
              tickFormatter={pct}
              stroke="var(--suave)"
              width={48}
            />
          )}
          <Tooltip
            labelFormatter={(t) => new Date(Number(t)).toLocaleString('pt-BR')}
            contentStyle={{ background: 'var(--fundo)', borderColor: 'var(--borda)', color: 'var(--texto)' }}
          />
          {horaria ? (
            <>
              <Bar
                yAxisId="uptime"
                dataKey="uptime"
                name="No ar na hora"
                fill="var(--grafico-uptime)"
                isAnimationActive={false}
              />
              <Area
                dataKey="faixa"
                name="p50–p95"
                stroke="none"
                fill="var(--grafico-faixa)"
                isAnimationActive={false}
              />
              <Line dataKey="p50" name="Mediana" stroke="var(--grafico-linha)" dot={false} isAnimationActive={false} />
            </>
          ) : (
            <>
              <Line
                dataKey="latencia"
                name="Latência"
                stroke="var(--grafico-linha)"
                dot={false}
                connectNulls={false}
                isAnimationActive={false}
              />
              <Scatter dataKey="fora" name="Fora do ar" fill="var(--estado-fora)" isAnimationActive={false} />
            </>
          )}
        </ComposedChart>
      </ResponsiveContainer>
    </figure>
  )
}
