import type { ValorDoIndicador } from './estado-do-alvo'

export type LinhaDoIndicador = { rotulo: string; valor: ValorDoIndicador }

// Os quatro KPIs do detalhe (spec do redesign, Telas > 2). min-h: as quatro
// formas têm a altura da mais alta, e nada pula quando uma vira outra. No
// celular, o rótulo ou o motivo pode quebrar em duas linhas, daí os 128 px;
// da largura sm em diante, cada um cabe em uma linha.
export function Indicadores({ linhas }: { linhas: readonly LinhaDoIndicador[] }) {
  return (
    <dl className="grid grid-cols-2 gap-4 lg:grid-cols-4">
      {linhas.map((l) => (
        <div key={l.rotulo} className="min-h-32 min-w-0 space-y-1 sm:min-h-28 rounded-padrao border border-borda bg-superficie p-4 sombra-nivel-1">
          <dt className="text-meta text-suave">{l.rotulo}</dt>
          <dd>
            <ConteudoDoIndicador valor={l.valor} />
          </dd>
        </div>
      ))}
    </dl>
  )
}

// Texto em 18 px: "Indisponível" a 28 px quebra no meio da palavra num cartão
// de celular. leading-9 é a altura da linha do número, para a forma não pular.
const TEXTO_NO_LUGAR_DO_NUMERO = 'text-lg leading-9 font-semibold text-suave'

function ConteudoDoIndicador({ valor }: { valor: ValorDoIndicador }) {
  switch (valor.forma) {
    case 'valor':
      return (
        <>
          <span className="text-3xl font-semibold">{valor.valor}</span>
          {valor.unidade && (
            <>
              {' '}
              <span className="text-meta font-normal text-suave">{valor.unidade}</span>
            </>
          )}
        </>
      )
    case 'carregando':
      // O leitor de tela ouve um "carregando" só, o do grupo.
      return <div aria-hidden="true" className="h-9 w-20 animate-pulse rounded-padrao bg-painel motion-reduce:animate-none" />
    case 'indisponivel':
      return <span className={TEXTO_NO_LUGAR_DO_NUMERO}>Indisponível</span>
    case 'sem-dados':
      return (
        <>
          <span className={`block ${TEXTO_NO_LUGAR_DO_NUMERO}`}>Sem dados</span>
          <span className="block text-meta text-suave">{valor.motivo}</span>
        </>
      )
  }
}
