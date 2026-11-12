import { cn } from '@/lib/utils'

// O esqueleto do painel: a manchete e oito linhas da tabela,
// no lugar onde vão aparecer. A animação para com prefers-reduced-motion
// (motion-reduce, e a regra global do index.css); o leitor de tela ouve
// "Carregando…", e o e2e espera o aria-busy sumir.
const BLOCO = 'animate-pulse rounded-padrao bg-painel motion-reduce:animate-none'
const LINHAS = [0, 1, 2, 3, 4, 5, 6, 7]

export function EsqueletoDoPainel() {
  return (
    <div aria-busy="true" className="space-y-6">
      <span className="sr-only">Carregando…</span>
      <div className="space-y-3">
        <div aria-hidden="true" className={cn(BLOCO, 'h-8 w-2/3')} />
        <div aria-hidden="true" className={cn(BLOCO, 'h-4 w-1/3')} />
      </div>
      <div className="space-y-1">
        {LINHAS.map((i) => (
          <div key={i} aria-hidden="true" className={cn(BLOCO, 'h-12')} />
        ))}
      </div>
    </div>
  )
}
