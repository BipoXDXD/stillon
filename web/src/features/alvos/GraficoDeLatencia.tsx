import { useId } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { Serie } from '@/lib/api/schemas'
import { dataHora, diaMes, horaMinuto } from '@/lib/format'
import { larguraDoEixo } from './eixo'
import { dominioX, escalaRobusta, marcasDoEixoY, textoDoPico, ticksDoEixoX } from './escala'
import { faixasForaDoAr, latenciasDaSerie, pontosBrutos, pontosHorarios, type PontoBruto, type PontoHorario } from './serie'

// Recharts, só aqui: este arquivo entra no chunk do detalhe, carregado sob
// demanda. As cores são variáveis CSS do index.css, e não um <style> gerado
// em runtime, que a CSP bloquearia sem nonce (spike do Plano 6, §8.1).
//
// O desenho da spec do redesign (Telas > 2): a linha da mediana em índigo, a
// faixa p50–p95 em primária 100, o "fora do ar" hachurado (forma + cor), as
// marcas do eixo X em horas redondas e a escala Y robusta, com o pico fora
// dela anotado na legenda. O que decide o desenho está em escala.ts e
// serie.ts, testado sem o Recharts.
//
// descritoPor: o id do resumo em texto, que descreve o gráfico.
export function GraficoDeLatencia({ serie, descritoPor }: { serie: Serie; descritoPor?: string }) {
  // O id entra num url(#…): só letras, dígitos, _ e -.
  const id = useId().replace(/[^\w-]/g, '')
  const idDaHachura = `hachura-${id}`
  const idDoPico = `pico-${id}`
  const horaria = serie.resolution === '1h'
  const dados: (PontoBruto | PontoHorario)[] = horaria ? pontosHorarios(serie.items) : pontosBrutos(serie.items)
  const faixas = faixasForaDoAr(serie)
  const dominio = dominioX(
    dados.map((p) => p.t),
    faixas,
  )
  if (dominio === null) return null
  const escala = escalaRobusta(latenciasDaSerie(serie))
  const eixo = ticksDoEixoX(dominio[0], dominio[1])
  const pico = textoDoPico(escala, eixo.porDia)
  const descricao = [descritoPor, pico ? idDoPico : undefined].filter(Boolean).join(' ') || undefined
  return (
    <div className="space-y-3">
      {/* O nome e a descrição ficam no <svg> do Recharts, que é o que recebe
          o foco (a accessibilityLayer: role="application", tabIndex 0). */}
      <figure className="h-64 w-full" data-testid="grafico-latencia">
        <ResponsiveContainer width="100%" height="100%">
          <ComposedChart
            data={dados}
            margin={{ top: 8, right: 8, bottom: 0, left: 0 }}
            aria-label="Gráfico de latência"
            aria-describedby={descricao}
          >
            <defs>
              <pattern id={idDaHachura} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
                <path d="M0 0V6" stroke="var(--estado-fora)" strokeWidth={2} />
              </pattern>
            </defs>
            <CartesianGrid stroke="var(--borda)" vertical={false} />
            <XAxis
              dataKey="t"
              type="number"
              scale="time"
              domain={dominio}
              ticks={eixo.marcas}
              tickFormatter={eixo.porDia ? diaMes : horaMinuto}
              stroke="var(--suave)"
            />
            {/* allowDataOverflow: o que passa do teto é cortado, e o pico vai
                para a legenda. O teto é a última marca, e a largura do eixo
                sai do rótulo dele: fixa, cortava 4 cifras e quebrava 5. */}
            <YAxis
              domain={[0, escala.teto]}
              ticks={marcasDoEixoY(escala.teto)}
              allowDataOverflow
              stroke="var(--suave)"
              unit=" ms"
              width={larguraDoEixo(`${escala.teto} ms`)}
            />
            <Tooltip
              labelFormatter={(t) => dataHora(new Date(Number(t)).toISOString())}
              contentStyle={{ background: 'var(--superficie)', borderColor: 'var(--borda)', color: 'var(--texto)' }}
            />
            {faixas.map((f) => (
              <ReferenceArea
                key={f.de}
                x1={f.de}
                x2={f.ate}
                fill={`url(#${idDaHachura})`}
                fillOpacity={1}
                stroke="none"
                ifOverflow="hidden"
              />
            ))}
            {horaria ? (
              <>
                <Area dataKey="faixa" name="p50–p95" stroke="none" fill="var(--grafico-faixa)" isAnimationActive={false} />
                <Line dataKey="p50" name="Mediana" stroke="var(--grafico-linha)" strokeWidth={2} dot={false} isAnimationActive={false} />
              </>
            ) : (
              <Line
                dataKey="latencia"
                name="Latência"
                stroke="var(--grafico-linha)"
                strokeWidth={2}
                dot={false}
                connectNulls={false}
                isAnimationActive={false}
              />
            )}
          </ComposedChart>
        </ResponsiveContainer>
      </figure>
      <Legenda horaria={horaria} comFalhas={faixas.length > 0} idDaHachura={idDaHachura} />
      {pico && (
        <p id={idDoPico} className="text-meta text-suave">
          {pico}
        </p>
      )}
    </div>
  )
}

// A legenda explícita: o leitor de tela já tem a tabela e o resumo, e a
// legenda diz o que cada forma significa para quem olha.
function Legenda({ horaria, comFalhas, idDaHachura }: { horaria: boolean; comFalhas: boolean; idDaHachura: string }) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-2 text-meta text-suave">
      <li className="flex items-center gap-2">
        <span aria-hidden="true" className="h-1 w-4 rounded-full bg-primaria" />
        {horaria ? 'Mediana' : 'Latência'}
      </li>
      {horaria && (
        <li className="flex items-center gap-2">
          <span aria-hidden="true" className="size-3 rounded-full bg-primaria-100" />
          p50–p95
        </li>
      )}
      {comFalhas && (
        <li className="flex items-center gap-2">
          <svg aria-hidden="true" width="16" height="12">
            <rect width="16" height="12" fill={`url(#${idDaHachura})`} />
          </svg>
          {horaria ? 'Horas com menos de 99% no ar' : 'Fora do ar'}
        </li>
      )}
    </ul>
  )
}
