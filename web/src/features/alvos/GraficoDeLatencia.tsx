import { useId, useState, type ReactNode } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { Serie } from '@/lib/api/schemas'
import { dataHora, diaMes, horaMinuto, milissegundos } from '@/lib/format'
import { larguraDoEixo } from './eixo'
import { dominioX, escalaRobusta, marcasDoEixoY, textoDoPico, ticksDoEixoX } from './escala'
import {
  comP95Reduzido,
  faixasForaDoAr,
  latenciasDaSerie,
  pontosBrutos,
  pontosHorarios,
  pontosPorGrupoDoP95,
  type PontoBruto,
  type PontoHorario,
} from './serie'

const MARGEM = { top: 8, right: 8, bottom: 0, left: 0 }
const TRACO_DO_P95 = '4 3'

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
  const [largura, setLargura] = useState(0)
  const idDaHachura = `hachura-${id}`
  const idDoPico = `pico-${id}`
  const horaria = serie.resolution === '1h'
  const pontosDaHora = horaria ? pontosHorarios(serie.items) : []
  const pontosDoCheck = horaria ? [] : pontosBrutos(serie.items)
  const pontos: (PontoBruto | PontoHorario)[] = horaria ? pontosDaHora : pontosDoCheck
  const faixas = faixasForaDoAr(serie)
  const dominio = dominioX(
    pontos.map((p) => p.t),
    faixas,
  )
  if (dominio === null) return null
  const escala = escalaRobusta(latenciasDaSerie(serie))
  const eixo = ticksDoEixoX(dominio[0], dominio[1])
  const pico = textoDoPico(escala, eixo.porDia)
  const descricao = [descritoPor, pico ? idDoPico : undefined].filter(Boolean).join(' ') || undefined
  const larguraDoY = larguraDoEixo(`${escala.teto} ms`)
  const porGrupo = pontosPorGrupoDoP95(pontosDaHora.length, largura - larguraDoY - MARGEM.right - MARGEM.left)
  const dados: (PontoBruto | PontoHorario)[] = horaria ? comP95Reduzido(pontosDaHora, porGrupo) : pontosDoCheck
  return (
    <div className="space-y-3">
      {/* O nome e a descrição ficam no <svg> do Recharts, que é o que recebe
          o foco (a accessibilityLayer: role="application", tabIndex 0). */}
      <figure className="h-64 w-full" data-testid="grafico-latencia">
        <ResponsiveContainer width="100%" height="100%" onResize={setLargura}>
          <ComposedChart
            data={dados}
            margin={MARGEM}
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
              width={larguraDoY}
            />
            {/* Pelo instante, nos pontos sem redução: o traço do p95 pode estar
                reduzido, e o tooltip diz o valor exato daquela hora. */}
            <Tooltip content={<ConteudoDoTooltip pontos={pontos} />} />
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
                {/* A faixa tem 1,09:1 sobre a superfície, e o tracejado é o
                    que a torna visível. Na Area, o contorno desenharia as
                    duas bordas; a mediana vem por último, por cima. */}
                <Area dataKey="faixa" name="p50–p95" stroke="none" fill="var(--grafico-faixa)" isAnimationActive={false} />
                <Line
                  dataKey="p95"
                  name="p95"
                  stroke="var(--borda-controle)"
                  strokeWidth={1.5}
                  strokeDasharray={TRACO_DO_P95}
                  dot={false}
                  connectNulls={porGrupo > 1}
                  tooltipType="none"
                  isAnimationActive={false}
                />
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

// Texto em cor neutra e a cor só na amostra da série: pintada com a cor da
// série, a faixa p50–p95 (tom 100) fica ilegível sobre a superfície. O label
// vem do Recharts, que clona o elemento: é o instante do ponto sob o cursor.
function ConteudoDoTooltip({ pontos, label }: { pontos: readonly (PontoBruto | PontoHorario)[]; label?: string | number }) {
  const ponto = pontos.find((p) => p.t === Number(label))
  if (ponto === undefined) return null
  return (
    <div className="min-w-44 space-y-1 rounded-padrao border border-borda bg-superficie p-3 text-sm sombra-nivel-2">
      <p className="text-meta text-suave">{dataHora(new Date(ponto.t).toISOString())}</p>
      {'p50' in ponto ? (
        <>
          <LinhaDoTooltip amostra={<AmostraDaLinha />} nome="Mediana" ms={ponto.p50} />
          <LinhaDoTooltip amostra={<AmostraDoP95 />} nome="p95" ms={ponto.p95} />
        </>
      ) : (
        <LinhaDoTooltip amostra={<AmostraDaLinha />} nome="Latência" ms={ponto.latencia} />
      )}
    </div>
  )
}

function LinhaDoTooltip({ amostra, nome, ms }: { amostra: ReactNode; nome: string; ms: number | null }) {
  return (
    <p className="flex items-center gap-2">
      {amostra}
      <span className="text-suave">{nome}</span>
      <span className="ml-auto font-semibold">{milissegundos(ms)}</span>
    </p>
  )
}

function AmostraDaLinha() {
  return <span aria-hidden="true" className="h-1 w-4 shrink-0 rounded-full bg-primaria" />
}

// A faixa com a opacidade que o Recharts dá à Area (0,6), e o traço do p95: a
// amostra tem de parecer o que está no gráfico.
function AmostraDoP95() {
  return (
    <svg aria-hidden="true" width="16" height="12" className="shrink-0">
      <rect y="3" width="16" height="9" fill="var(--grafico-faixa)" fillOpacity={0.6} />
      <line x1="0" x2="16" y1="3" y2="3" stroke="var(--borda-controle)" strokeWidth={1.5} strokeDasharray={TRACO_DO_P95} />
    </svg>
  )
}

// A legenda explícita: o leitor de tela já tem a tabela e o resumo, e a
// legenda diz o que cada forma significa para quem olha.
function Legenda({ horaria, comFalhas, idDaHachura }: { horaria: boolean; comFalhas: boolean; idDaHachura: string }) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-2 text-meta text-suave">
      <li className="flex items-center gap-2">
        <AmostraDaLinha />
        {horaria ? 'Mediana' : 'Latência'}
      </li>
      {horaria && (
        <li className="flex items-center gap-2">
          <AmostraDoP95 />
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
