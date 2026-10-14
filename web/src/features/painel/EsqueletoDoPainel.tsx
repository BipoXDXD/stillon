import { cn } from '@/lib/utils'

// O esqueleto do painel (auditoria P12): a manchete, dois cartões de
// problema e quatro linhas, no lugar onde vão aparecer. A animação para com
// prefers-reduced-motion (motion-reduce, e a regra global do index.css); o
// leitor de tela ouve "Carregando…", e o e2e espera o aria-busy sumir.
const BLOCO = 'animate-pulse rounded-padrao bg-painel motion-reduce:animate-none'

export function EsqueletoDoPainel() {
  return (
    <div aria-busy="true" className="space-y-8">
      <span className="sr-only">Carregando…</span>
      <div className="space-y-3">
        <div aria-hidden="true" className={cn(BLOCO, 'h-8 w-2/3')} />
        <div aria-hidden="true" className={cn(BLOCO, 'h-4 w-1/3')} />
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {[0, 1].map((i) => (
          <div key={i} aria-hidden="true" className={cn(BLOCO, 'h-32 border-l-4 border-l-borda')} />
        ))}
      </div>
      <div className="space-y-1">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} aria-hidden="true" className={cn(BLOCO, 'h-11')} />
        ))}
      </div>
    </div>
  )
}
