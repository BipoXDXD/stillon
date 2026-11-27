import type { Projeto } from '@/lib/api/schemas'

// O último projeto aberto neste navegador: a raiz volta a ele.
export const CHAVE_DO_ULTIMO_PROJETO = 'stillon.projeto-atual'

// O projeto da raiz: o lembrado, se a pessoa ainda for membro dele; senão o
// padrão; sem padrão, o primeiro. Lista vazia não tem para onde ir.
// is_default é do projeto, não da pessoa: membro do padrão de outro titular vê
// dois, e o dela (role owner) vem antes.
export function escolherProjeto(lista: readonly Projeto[], lembrado: string | null): Projeto | null {
  return (
    lista.find((p) => p.id === lembrado) ??
    lista.find((p) => p.is_default && p.role === 'owner') ??
    lista.find((p) => p.is_default) ??
    lista[0] ??
    null
  )
}

// O localStorage lança em aba privada, com cookies bloqueados ou cota cheia:
// sem ele, a raiz só não lembra o último projeto.
export function lerProjetoLembrado(): string | null {
  try {
    return localStorage.getItem(CHAVE_DO_ULTIMO_PROJETO)
  } catch {
    return null
  }
}

export function lembrarProjeto(id: string): void {
  try {
    localStorage.setItem(CHAVE_DO_ULTIMO_PROJETO, id)
  } catch {
    // Mesmo motivo: lembrar é conveniência, não requisito.
  }
}
