import { createContext, useCallback, useContext, useEffect, useMemo } from 'react'
import { Outlet, useParams } from 'react-router'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import type { ApiDoProjeto } from '@/lib/api/cliente'
import { useProjetos } from '@/lib/api/consultas'
import type { Papel, Projeto } from '@/lib/api/schemas'
import { useApi } from './contexto'
import { lembrarProjeto } from './escolher-projeto'
import { NaoEncontrada } from './NaoEncontrada'

export type ProjetoAtual = { id: string; nome: string; papel: Papel; padrao: boolean }

const Contexto = createContext<ProjetoAtual | null>(null)

// A rota /projects/:projectId: o projeto da URL precisa estar na lista de
// projetos da pessoa. Fora dela (apagado, ou de que ela não é membro), a
// página não encontrada, e as telas do projeto nem montam: nenhuma consulta
// de recurso sai para um projeto que não é dela.
export function ProvedorDoProjeto() {
  const { projectId } = useParams()
  const projetos = useProjetos()
  return (
    <EstadoDaConsulta consulta={projetos}>
      {({ items }) => {
        const projeto = items.find((p) => p.id === projectId)
        return projeto ? <ComProjeto projeto={projeto} /> : <NaoEncontrada />
      }}
    </EstadoDaConsulta>
  )
}

function ComProjeto({ projeto }: { projeto: Projeto }) {
  const { id, name, role, is_default } = projeto
  const atual = useMemo<ProjetoAtual>(() => ({ id, nome: name, papel: role, padrao: is_default }), [id, name, role, is_default])
  useEffect(() => lembrarProjeto(id), [id])
  return (
    <Contexto.Provider value={atual}>
      <Outlet />
    </Contexto.Provider>
  )
}

export function useProjeto(): ProjetoAtual {
  const p = useContext(Contexto)
  if (!p) throw new Error('fora do ProvedorDoProjeto')
  return p
}

export function useApiDoProjeto(): ApiDoProjeto {
  const api = useApi()
  const { id } = useProjeto()
  return useMemo(() => api.doProjeto(id), [api, id])
}

// O caminho de uma tela dentro do projeto atual: '/targets/1' vira
// '/projects/{id}/targets/1'.
export function useRotaDoProjeto(): (rota: `/${string}`) => string {
  const { id } = useProjeto()
  return useCallback((rota) => `/projects/${encodeURIComponent(id)}${rota}`, [id])
}
