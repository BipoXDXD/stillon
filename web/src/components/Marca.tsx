import { cn } from '@/lib/utils'

// A marca do StillOn (spec do redesign, seções 1 e 5): ponto índigo e o nome
// em Fraunces. O ponto é decorativo; o nome é texto. Normal no cabeçalho
// (20 px), grande no login (28 px).
export function Marca({ tamanho = 'normal' }: { tamanho?: 'normal' | 'grande' }) {
  const grande = tamanho === 'grande'
  return (
    <span className="inline-flex items-center gap-2">
      <span aria-hidden="true" className={cn('shrink-0 rounded-full bg-primaria', grande ? 'size-4' : 'size-3')} />
      <span className={cn('font-display font-semibold text-texto', grande ? 'text-3xl' : 'text-xl')}>StillOn</span>
    </span>
  )
}
