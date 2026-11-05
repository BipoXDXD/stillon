import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { Minigrafico } from '@/components/Minigrafico'
import { IconeDoEstado, SeloDeEstado } from '@/components/SeloDeEstado'
import type { EstadoExibido } from '@/lib/estado'
import { porcento } from '@/lib/format'
import { cn } from '@/lib/utils'
import { tagsVisiveis } from './filtros'
import { medidaDaLinha, situacaoNaLinhaDoNome, type LinhaDeMonitor } from './linhas'

// A tabela do painel denso: uma linha de 48 px por monitor (com 44, a área de toque de um nome encosta na do vizinho), com o cabeçalho
// uma vez só. As colunas somem por largura (spec, tabela de colunas); o estado
// nunca some: abaixo de lg ele é o ícone junto do nome, com o texto para o
// leitor de tela.
const CELULA = 'px-2 sm:px-4'
const SO_DESKTOP = 'hidden lg:table-cell'
const DE_SM = 'hidden sm:table-cell'

const ACENTOS: Partial<Record<EstadoExibido, string>> = { down: 'acento-fora', degraded: 'acento-degradado' }

export function TabelaDeMonitores({ linhas, agora }: { linhas: LinhaDeMonitor[]; agora: Date }) {
  return (
    <div className="rounded-padrao border border-borda bg-superficie sombra-nivel-1">
      <table className="w-full text-sm">
        <caption className="sr-only">
          Monitores: estado, tags, situação, latência do último check, tempo no ar em 24 h e as últimas 24 horas
        </caption>
        <thead>
          <tr className="text-meta text-apagado">
            <th scope="col" className={cn(SO_DESKTOP, CELULA, 'py-2 text-left font-normal')}>Estado</th>
            <th scope="col" className={cn(CELULA, 'py-2 text-left font-normal sm:min-w-44')}>Alvo</th>
            <th scope="col" className={cn(SO_DESKTOP, CELULA, 'py-2 text-left font-normal')}>Tags</th>
            <th scope="col" className={cn(DE_SM, CELULA, 'py-2 text-left font-normal')}>Situação</th>
            <th scope="col" className={cn(DE_SM, CELULA, 'py-2 text-right font-normal')}>Latência</th>
            <th scope="col" className={cn(CELULA, 'py-2 text-right font-normal')}>24 h</th>
            <th scope="col" className={cn(CELULA, 'py-2 text-left font-normal')}>Últimas 24 h</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-borda border-t border-borda">
          {linhas.map((linha) => (
            <LinhaDaTabela key={linha.chave} linha={linha} agora={agora} />
          ))}
        </tbody>
      </table>
    </div>
  )
}

function LinhaDaTabela({ linha, agora }: { linha: LinhaDeMonitor; agora: Date }) {
  const acento = ACENTOS[linha.estado]
  const situacaoAbaixo = situacaoNaLinhaDoNome(linha)
  const parado = linha.estado === 'unknown' || linha.estado === 'pausado'
  return (
    <tr className="h-12">
      <td className={cn(SO_DESKTOP, CELULA, acento)}>
        <SeloDeEstado estado={linha.estado} />
      </td>
      <th scope="row" className={cn(CELULA, acento, 'text-left font-normal lg:sem-acento')}>
        <span className="flex min-h-11 items-center gap-2">
          <IconeDoEstado estado={linha.estado} className="lg:hidden" />
          <Link
            to={linha.href}
            className={cn('alvo-de-toque min-w-0 font-semibold underline-offset-4 hover:underline', parado && 'text-suave')}
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
      <td className={cn(DE_SM, CELULA, 'text-right whitespace-nowrap')}>{medidaDaLinha(linha)}</td>
      <td className={cn(CELULA, 'text-right whitespace-nowrap')}>{linha.uptime24h === null ? '—' : porcento(linha.uptime24h)}</td>
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
