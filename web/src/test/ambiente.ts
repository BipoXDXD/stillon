import { criarApi } from '@/lib/api/cliente'
import type { Buscar } from '@/lib/api/http'
import { criarSessao, type Canal, type Travar } from '@/lib/api/sessao'
import { criarApiFalsa } from './api-falsa'
import { servidor } from './setup'

// O fetch do Node não resolve caminho relativo; o do navegador resolve
// contra a origem da página. Este buscar faz o que o navegador faria.
export const buscarDoTeste = (entrada: string, init: RequestInit) => fetch(new URL(entrada, location.origin), init)

export function usarApiFalsa() {
  const falsa = criarApiFalsa()
  servidor.use(...falsa.handlers)
  return falsa
}

export function novaSessao(o: { travar?: Travar; canal?: Canal | null; buscar?: Buscar; prazoMs?: number } = {}) {
  const buscar = o.buscar ?? buscarDoTeste
  const sessao = criarSessao({ buscar, travar: o.travar, canal: o.canal ?? null, prazoMs: o.prazoMs })
  const api = criarApi(sessao, buscar)
  return { sessao, api }
}

// Uma trava com a semântica de navigator.locks.request: exclusiva e em
// ordem de chegada. Várias "abas" dividem a mesma.
export function travaFalsa(): Travar {
  let fila: Promise<unknown> = Promise.resolve()
  return <T,>(_nome: string, fn: () => Promise<T>) => {
    const vez = fila.then(fn, fn)
    fila = vez.catch(() => undefined)
    return vez
  }
}
