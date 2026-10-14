import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// O tailwind-merge só conhece a escala padrão do Tailwind. Sem isto, o
// "text-meta" (13 px, do index.css) passa por cor e some ao lado de
// "text-suave", e o "rounded-padrao" não substitui outro raio.
const mesclar = extendTailwindMerge({ extend: { theme: { text: ['meta'], radius: ['padrao'] } } })

export function cn(...classes: ClassValue[]) {
  return mesclar(clsx(classes))
}
