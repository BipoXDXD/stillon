import { criarApi } from './cliente'
import { canalDoNavegador, criarSessao, travaDoNavegador } from './sessao'

// As instâncias do painel. Uma sessão por aba (o módulo é carregado uma vez),
// com a trava e o canal do navegador; os testes montam as deles.
const buscar = (entrada: string, init: RequestInit) => fetch(entrada, init)

export const sessao = criarSessao({ buscar, travar: travaDoNavegador(), canal: canalDoNavegador() })
export const api = criarApi(sessao, buscar)

export { ApiError } from './http'
export type { Api } from './cliente'
export type { Sessao } from './sessao'
