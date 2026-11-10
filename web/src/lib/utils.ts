import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// O tailwind-merge só conhece a escala padrão do Tailwind. Sem isto, o
// "text-meta" (13 px, do index.css) passa por cor e some ao lado de
// "text-suave", e o "rounded-padrao" não substitui outro raio.
const mesclar = extendTailwindMerge({ extend: { theme: { text: ['meta'], radius: ['padrao'] } } })

// O ramo default de um switch exaustivo: com um caso novo na união, o
// compilador para aqui; em tempo de execução, um valor fora do tipo falha alto.
export function assertUnreachable(valor: never): never {
  throw new Error(`Valor inesperado: ${JSON.stringify(valor)}`)
}

export function cn(...classes: ClassValue[]) {
  return mesclar(clsx(classes))
}
