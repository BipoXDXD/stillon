import { SeloDeEstado } from '@/components/SeloDeEstado'
import type { EstadoAlvo } from '@/lib/api/schemas'
import { estadoExibido } from '@/lib/estado'
import { cn } from '@/lib/utils'
import { faixaDaPagina } from './faixa'

// estado null: o alvo não está no resumo (apagado, ou o resumo ainda não chegou).
export type ItemDaPrevia = {
  readonly id: string
  readonly nome: string
  readonly estado: EstadoAlvo | null
  readonly ativo: boolean
}

// A faixa com o fundo 100 do estado, o texto na cor dele (pares do
// tokens.test.ts) e a borda de acento, como na página pública.
const TONS_DA_FAIXA: Record<EstadoAlvo, string> = {
  up: 'border-l-no-ar bg-no-ar-100 text-no-ar',
  degraded: 'border-l-degradado bg-degradado-100 text-degradado',
  down: 'border-l-fora bg-fora-100 text-fora',
  unknown: 'border-l-sem-dados bg-sem-dados-100 text-sem-dados',
}

// Prévia da página pública (auditoria SP3; spec do Plano 6, D23): o papel, o
// título em Fraunces, a mesma faixa de estado e um cartão por alvo. A página
// real é HTML do servidor, só acessível depois de publicada, e o painel não
// tem os 90 dias de cada alvo: a barra vira um contorno, e a nota diz o que
// falta.
export function PreviaDaPagina({ titulo, itens }: { titulo: string; itens: readonly ItemDaPrevia[] }) {
  // A página pública não sabe de pausa: alvo pausado ou fora do resumo sai
  // "sem dados" (targetstate.Current), e a faixa segue a mesma regra.
  const faixa = faixaDaPagina(
    itens.map((it) => ({ nome: it.nome || '—', estado: it.ativo && it.estado ? it.estado : 'unknown' })),
  )
  return (
    <section aria-labelledby="titulo-previa" className="space-y-4 rounded-padrao border border-borda bg-fundo p-4 sm:p-6">
      <h2 id="titulo-previa" className="rotulo-de-grupo font-sans text-apagado">
        Prévia aproximada
      </h2>
      <p className="font-display text-2xl font-semibold">{titulo.trim() || 'Título da página'}</p>
      <div className={cn('space-y-1 rounded-padrao border-l-4 p-4', TONS_DA_FAIXA[faixa.estado])}>
        <p className="font-display text-xl font-semibold">{faixa.frase}</p>
        {faixa.detalhe && <p className="text-sm">{faixa.detalhe}</p>}
      </div>
      <ul className="space-y-3">
        {itens.map((it) => (
          <li key={it.id} className="space-y-3 rounded-padrao border border-borda bg-superficie p-4">
            <div className="flex items-start justify-between gap-4">
              {/* <p>, e não <span>: o overflow-wrap do index.css quebra o
                  nome de 100 caracteres sem espaço (e2e/a11y.spec.ts). */}
              <p className="min-w-0 font-semibold">{it.nome || '—'}</p>
              {it.estado ? (
                <SeloDeEstado estado={estadoExibido(it.estado, it.ativo)} />
              ) : (
                <span className="text-suave">—</span>
              )}
            </div>
            <div aria-hidden="true" className="h-8 rounded-padrao border border-dashed border-borda-controle" />
          </li>
        ))}
      </ul>
      <p className="text-sm text-suave">As barras dos últimos 90 dias e o histórico de incidentes aparecem na página publicada.</p>
    </section>
  )
}
