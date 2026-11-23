import { Check } from 'lucide-react'
import { DropdownMenu } from 'radix-ui'
import type { ComponentProps } from 'react'
import { cn } from '@/lib/utils'

// O menu suspenso do painel, sobre o DropdownMenu do Radix: o teclado (setas,
// Home/End, a inicial, Esc devolvendo o foco ao gatilho) e os papéis de ARIA
// vêm dele. Flutua como o diálogo e o aviso: superfície elevada e sombra de
// nível 2. Itens de 44 px (alvo de toque); o destacado, pelo teclado ou pelo
// ponteiro, na primária 100. Como o Dialogo, o react-remove-scroll do modo
// modal injeta um <style> que passa pela CSP com o nonce de src/lib/nonce.ts.

export const Menu = DropdownMenu.Root
export const GatilhoDoMenu = DropdownMenu.Trigger

// collisionPadding 16: no celular a caixa nunca encosta na borda da tela. A
// altura vai até 60% da tela, e o resto rola.
export function ConteudoDoMenu({ className, ...props }: ComponentProps<typeof DropdownMenu.Content>) {
  return (
    <DropdownMenu.Portal>
      <DropdownMenu.Content
        sideOffset={4}
        collisionPadding={16}
        className={cn(
          'z-40 max-h-[60vh] min-w-56 overflow-y-auto rounded-padrao border border-borda bg-superficie-elevada p-1 sombra-nivel-2',
          className,
        )}
        {...props}
      />
    </DropdownMenu.Portal>
  )
}

const item =
  'flex min-h-11 cursor-default items-center gap-2 rounded-padrao px-3 text-sm text-texto outline-none select-none data-highlighted:bg-primaria-100 data-disabled:opacity-60'

export function ItemDoMenu({ className, ...props }: ComponentProps<typeof DropdownMenu.Item>) {
  return <DropdownMenu.Item className={cn(item, className)} {...props} />
}

export const GrupoDeOpcoesDoMenu = DropdownMenu.RadioGroup

// A opção marcada leva o ✓ à esquerda (aria-checked vem do Radix); as outras
// guardam o lugar dele, e os nomes ficam alinhados.
export function OpcaoDoMenu({ className, children, ...props }: ComponentProps<typeof DropdownMenu.RadioItem>) {
  return (
    <DropdownMenu.RadioItem className={cn(item, 'py-2', className)} {...props}>
      <span aria-hidden="true" className="flex size-4 shrink-0 items-center justify-center">
        <DropdownMenu.ItemIndicator>
          <Check className="size-4 text-primaria" />
        </DropdownMenu.ItemIndicator>
      </span>
      {children}
    </DropdownMenu.RadioItem>
  )
}

export function SeparadorDoMenu({ className, ...props }: ComponentProps<typeof DropdownMenu.Separator>) {
  return <DropdownMenu.Separator className={cn('my-1 h-px bg-borda', className)} {...props} />
}
