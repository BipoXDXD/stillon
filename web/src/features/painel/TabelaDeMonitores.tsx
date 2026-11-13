import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { aoApontarPreCarregar, carregarDetalhe } from '@/app/rotas-sob-demanda'
import { Minigrafico } from '@/components/Minigrafico'
import { IconeDoEstado, SeloDeEstado } from '@/components/SeloDeEstado'
import type { EstadoExibido } from '@/lib/estado'
import { porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { tagsVisiveis } from './filtros'
import { TEXTO_DA_ZONA, zonaDoEstado, type Zona } from './grupos'
import { medidaDaLinha, situacaoNaLinhaDoNome, type LinhaDeMonitor } from './linhas'

// A tabela do painel denso: uma linha de 48 px por monitor (56 no problema;
// com 44, a área de toque de um nome encosta na do vizinho), com o cabeçalho
// uma vez só. As colunas somem por largura (spec, tabela de colunas); o estado
// nunca some: abaixo de lg ele é o ícone junto do nome, com o texto para o
// leitor de tela.
const CELULA = 'px-2 sm:px-4'
const SO_DESKTOP = 'hidden lg:table-cell'
const DE_SM = 'hidden sm:table-cell'

const ACENTOS: Partial<Record<EstadoExibido, string>> = { down: 'acento-fora', degraded: 'acento-degradado' }

// Três zonas de densidade (RUI): o problema ganha altura e o nome em 600; no
// ar e parado ficam rebaixados, com o nome em 400 e as medidas em suave (4,5:1
// nos dois temas). O parado leva também o nome em suave.
const ZONAS: Record<Zona, { linha: string; nome: string; medida: string }> = {
  problema: { linha: 'h-14', nome: 'font-semibold', medida: '' },
  'no-ar': { linha: 'h-12', nome: 'font-normal', medida: 'text-suave' },
  parado: { linha: 'h-12', nome: 'font-normal text-suave', medida: 'text-suave' },
}

const COLUNAS = 7

// blocos: a lista já ordenada, cortada em blocos (as zonas, na ordem por
// gravidade, cada uma com o seu título; nas outras ordens, um bloco sem
// zona). Cada bloco é um <tbody>, e o vão entre eles é uma faixa da cor do
// fundo, como se fossem cartões separados. daLista: os filtros e a ordem na
// URL, que o link do detalhe leva junto para o Anterior/Próximo de lá.
type Props = { blocos: readonly { zona: Zona | null; linhas: readonly LinhaDeMonitor[] }[]; daLista: string; agora: Date }

export function TabelaDeMonitores({ blocos, daLista, agora }: Props) {
  return (
    <div className="rounded-padrao border border-borda bg-superficie sombra-nivel-1">
      <table className="w-full text-sm">
        <caption className="sr-only">
          Monitores: estado, tags, situação, latência do último check, tempo no ar em 24 h e as últimas 24 horas
        </caption>
        <thead>
          <tr className="text-meta text-apagado">
            <th scope="col" className={cn(SO_DESKTOP, CELULA, 'py-2 text-left font-normal')}>Estado</th>
            <th scope="col" className={cn(CELULA, 'py-2 text-left font-normal sm:min-w-44')}>Monitor</th>
            <th scope="col" className={cn(SO_DESKTOP, CELULA, 'py-2 text-left font-normal')}>Tags</th>
            <th scope="col" className={cn(DE_SM, CELULA, 'py-2 text-left font-normal')}>Situação</th>
            <th scope="col" className={cn(DE_SM, CELULA, 'py-2 text-right font-normal')}>Latência</th>
            <th scope="col" className={cn(CELULA, 'py-2 text-right font-normal')}>24 h</th>
            <th scope="col" className={cn(CELULA, 'py-2 text-left font-normal')}>Últimas 24 h</th>
          </tr>
        </thead>
        {blocos.map(({ zona, linhas }, i) => (
          <tbody key={zona ?? i} className={cn('divide-y divide-borda', i === 0 ? 'border-t border-borda' : 'border-t-8 border-fundo')}>
            {zona && (
              // Um <h3> numa célula, e não um <th scope="rowgroup">: o leitor
              // de tela pula de seção pelos títulos, e a contagem de cabeçalhos
              // de linha segue um por monitor.
              <tr>
                <td colSpan={COLUNAS} className={cn(CELULA, 'pt-4 pb-2')}>
                  <h3 className="rotulo-de-grupo font-sans text-apagado">
                    {TEXTO_DA_ZONA[zona]} · {linhas.length}
                  </h3>
                </td>
              </tr>
            )}
            {linhas.map((linha) => (
              <LinhaDaTabela key={linha.chave} linha={linha} daLista={daLista} agora={agora} />
            ))}
          </tbody>
        ))}
      </table>
    </div>
  )
}

function LinhaDaTabela({ linha, daLista, agora }: { linha: LinhaDeMonitor; daLista: string; agora: Date }) {
  const acento = ACENTOS[linha.estado]
  const situacaoAbaixo = situacaoNaLinhaDoNome(linha)
  const zona = ZONAS[zonaDoEstado(linha.estado)]
  return (
    <tr className={zona.linha}>
      <td className={cn(SO_DESKTOP, CELULA, acento)}>
        <SeloDeEstado estado={linha.estado} texto={linha.textoDoEstado} />
      </td>
      <th scope="row" className={cn(CELULA, acento, 'text-left font-normal lg:sem-acento')}>
        <span className="flex min-h-11 items-center gap-2">
          <IconeDoEstado estado={linha.estado} texto={linha.textoDoEstado} className="lg:hidden" />
          <Link
            to={{ pathname: linha.href, search: daLista }}
            {...(linha.tipo === 'http' && aoApontarPreCarregar(carregarDetalhe))}
            className={cn('alvo-de-toque min-w-0 underline-offset-4 hover:underline', zona.nome)}
          >
            {linha.nome}
          </Link>
        </span>
        {situacaoAbaixo && <span className="block pb-2 text-meta font-normal text-suave sm:hidden">{situacaoAbaixo}</span>}
      </th>
      <td className={cn(SO_DESKTOP, CELULA)}>
        <TagsDaLinha tags={linha.tags} />
      </td>
      <td className={cn(DE_SM, CELULA)}>
        <span className="flex flex-wrap items-baseline gap-x-3">
          <span className="text-suave">{linha.situacao}</span>
          {linha.incidenteHref && (
            <Link
              to={linha.incidenteHref}
              aria-label={`Ver incidente de ${linha.nome}`}
              className="alvo-de-toque font-semibold whitespace-nowrap text-primaria underline underline-offset-4"
            >
              Ver incidente
            </Link>
          )}
        </span>
      </td>
      <td className={cn(DE_SM, CELULA, 'text-right whitespace-nowrap', zona.medida)}>{medidaDaLinha(linha)}</td>
      <td className={cn(CELULA, 'text-right whitespace-nowrap', zona.medida)}>
        {linha.uptime24h === null ? '—' : porcento(linha.uptime24h)}
      </td>
      <td className={CELULA}>
        <Minigrafico horas={linha.historico} agora={agora} />
      </td>
    </tr>
  )
}

function TagsDaLinha({ tags }: { tags: readonly string[] }) {
  const { visiveis, resto } = tagsVisiveis(tags)
  return (
    <span className="flex flex-wrap gap-1">
      {visiveis.map((tag) => (
        <PilulaDeTag key={tag}>{tag}</PilulaDeTag>
      ))}
      {resto.length > 0 && (
        <PilulaDeTag>
          <span aria-hidden="true">+{resto.length}</span>
          <span className="sr-only">mais {resto.join(', ')}</span>
        </PilulaDeTag>
      )}
    </span>
  )
}

function PilulaDeTag({ children }: { children: ReactNode }) {
  return <span className="rounded-full bg-painel px-2 text-meta whitespace-nowrap text-suave">{children}</span>
}
