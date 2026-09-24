import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { createContext, useContext, useEffect, useState, useSyncExternalStore, type ReactNode } from 'react'
import type { Api } from '@/lib/api/cliente'
import { ApiError } from '@/lib/api/http'
import type { Sessao } from '@/lib/api/sessao'

type Painel = { sessao: Sessao; api: Api }

const Contexto = createContext<Painel | null>(null)

// Só se repete o que pode melhorar repetindo: rede caída e 5xx (inclusive o
// 502 do proxy, que chega como 'unexpected' com status 502). 4xx não melhora:
// o 401 já foi tratado no cliente, e 404 ou 429 repetidos só gastam o limite
// por usuário. Resposta fora do contrato (status 200, 'unexpected') também
// não: o mesmo servidor responderia o mesmo. E um erro que não é ApiError é
// defeito do painel, não da rede.
export function podeRepetir(e: unknown): boolean {
  return e instanceof ApiError && (e.code === 'network' || e.status >= 500)
}

export function criarClienteDeConsultas() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: (n, e) => podeRepetir(e) && n < 2,
        staleTime: 10_000,
        refetchOnWindowFocus: true,
        // Aba em segundo plano não consulta (spec §11.4).
        refetchIntervalInBackground: false,
      },
      mutations: { retry: false },
    },
  })
}

// Quando a sessão acaba, nada da sessão anterior pode sobrar no cache para
// a próxima (outro usuário no mesmo navegador, ou o mesmo depois de sair).
function LimparAoEncerrar({ sessao }: { sessao: Sessao }) {
  const qc = useQueryClient()
  const estado = useSyncExternalStore(sessao.assinar, sessao.estado)
  useEffect(() => {
    if (estado === 'anonimo') qc.clear()
  }, [estado, qc])
  return null
}

export function ProvedorDoPainel({
  sessao,
  api,
  cliente,
  children,
}: Painel & { cliente?: QueryClient; children: ReactNode }) {
  const [qc] = useState(() => cliente ?? criarClienteDeConsultas())
  return (
    <Contexto.Provider value={{ sessao, api }}>
      <QueryClientProvider client={qc}>
        <LimparAoEncerrar sessao={sessao} />
        {children}
      </QueryClientProvider>
    </Contexto.Provider>
  )
}

function usePainel(): Painel {
  const p = useContext(Contexto)
  if (!p) throw new Error('fora do ProvedorDoPainel')
  return p
}

export function useApi(): Api {
  return usePainel().api
}

export function useSessao() {
  const { sessao } = usePainel()
  const estado = useSyncExternalStore(sessao.assinar, sessao.estado)
  return { sessao, estado, motivo: sessao.motivo() }
}
