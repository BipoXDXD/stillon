// As rotas que chegam sob demanda: o detalhe do alvo (Recharts, quase metade
// do bundle) e as configurações (QR code). Um import() só por chunk, dividido
// pela rota e pela pré-carga.
export const carregarDetalhe = () => import('@/features/alvos/PaginaDetalhe')
export const carregarConfiguracoes = () => import('@/features/configuracoes/PaginaConfiguracoes')

// Sem requestIdleCallback (Safari), uma folga para a tela atual terminar.
const FOLGA_SEM_OCIOSO_MS = 2000

// A pré-carga é um adiantamento: se falhar (um deploy novo troca o hash do
// chunk), a navegação repete o import e, aí sim, mostra o erro da rota.
export function preCarregar(carregar: () => Promise<unknown>): void {
  carregar().catch(() => undefined)
}

export function aoApontarPreCarregar(carregar: () => Promise<unknown>) {
  const pedir = () => preCarregar(carregar)
  return { onPointerEnter: pedir, onFocus: pedir }
}

// Network Information API, fora do lib.dom: só Chromium a expõe.
declare global {
  interface Navigator {
    readonly connection?: { readonly saveData?: boolean }
  }
}

// Hover e foco não existem no toque: em ocioso, o chunk vem sem pedir. Com a
// economia de dados ligada, só o que a pessoa aponta é baixado.
export function preCarregarEmOcioso(...cargas: (() => Promise<unknown>)[]): () => void {
  if (navigator.connection?.saveData === true) return () => undefined
  return quandoOcioso(() => {
    for (const carga of cargas) preCarregar(carga)
  })
}

export function quandoOcioso(fazer: () => void): () => void {
  if (typeof window.requestIdleCallback === 'function') {
    const id = window.requestIdleCallback(fazer)
    return () => window.cancelIdleCallback(id)
  }
  const relogio = setTimeout(fazer, FOLGA_SEM_OCIOSO_MS)
  return () => clearTimeout(relogio)
}
