import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { mensagemDe } from '@/lib/api/mensagens'
import { dataHora } from '@/lib/format'
import { Botao } from './ui/botao'

// O esqueleto da spec §4: blocos no lugar do conteúdo, nunca um spinner
// solto. A animação para com prefers-reduced-motion (motion-reduce, e a regra
// global do index.css); o leitor de tela ouve "Carregando…".
export function Esqueleto({ linhas = 3 }: { linhas?: number }) {
  return (
    <div aria-busy="true" className="space-y-3">
      <span className="sr-only">Carregando…</span>
      {Array.from({ length: linhas }, (_, i) => (
        <div
          key={i}
          aria-hidden="true"
          className="h-16 animate-pulse rounded-lg bg-painel motion-reduce:animate-none"
        />
      ))}
    </div>
  )
}

// Carregando / erro / conteúdo, igual em toda tela. Uma consulta que já tem
// dados e falha num poll de fundo continua mostrando os dados, com o aviso
// de que estão velhos: esconder o painel inteiro por um 502 de passagem
// seria pior do que mostrar o que se sabia há 15 segundos.
export function EstadoDaConsulta<T>({
  consulta,
  children,
}: {
  consulta: Pick<UseQueryResult<T>, 'data' | 'error' | 'isPending' | 'refetch' | 'dataUpdatedAt'>
  children: (dados: T) => ReactNode
}) {
  if (consulta.isPending) return <Esqueleto />
  const tentar = (
    <Botao tipo="secundario" tamanho="pequeno" onClick={() => void consulta.refetch()}>
      Tentar de novo
    </Botao>
  )
  if (consulta.data === undefined) {
    return (
      <div role="alert" className="space-y-2 rounded-md border border-perigo p-4">
        <p>{mensagemDe(consulta.error)}</p>
        {tentar}
      </div>
    )
  }
  return (
    <>
      {consulta.error && (
        // <output>: a região de status (role implícito), anunciada sem o foco
        // sair do lugar. Só conteúdo de frase dentro dela, daí o <span>.
        <output className="flex flex-wrap items-center gap-3 rounded-md border border-borda p-3 text-sm">
          <span>
            Não foi possível atualizar agora ({mensagemDe(consulta.error)}). Mostrando os dados de{' '}
            {dataHora(new Date(consulta.dataUpdatedAt).toISOString())}.
          </span>
          {tentar}
        </output>
      )}
      {children(consulta.data)}
    </>
  )
}
