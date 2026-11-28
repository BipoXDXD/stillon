import { createContext, useCallback, useContext, useEffect, useMemo, type ReactNode } from 'react'
import { Outlet, useMatch, useParams } from 'react-router'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { intervaloMinimoDo } from '@/features/alvos/esquema'
import type { ApiDoProjeto } from '@/lib/api/cliente'
import { useProjetos } from '@/lib/api/consultas'
import type { Papel, Projeto } from '@/lib/api/schemas'
import { useApi } from './contexto'
import { escolherProjeto, lembrarProjeto, lerProjetoLembrado } from './escolher-projeto'
import { NaoEncontrada } from './NaoEncontrada'

// intervaloMinimo: o do plano do titular do projeto (limits), em segundos.
export type ProjetoAtual = { id: string; nome: string; papel: Papel; padrao: boolean; intervaloMinimo: number }

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

// A key no Outlet: trocar de /projects/A/… para /projects/B/… remonta as
// telas. Sem ela o React reaproveita a tela de A, com o estado local (um campo
// digitado, um diálogo aberto) e o que estava em voo.
function ComProjeto({ projeto }: { projeto: Projeto }) {
  useEffect(() => lembrarProjeto(projeto.id), [projeto.id])
  return (
    <NoProjeto projeto={projeto}>
      <Outlet key={projeto.id} />
    </NoProjeto>
  )
}

// O projeto para quem está abaixo: as telas do projeto e, na moldura, as abas
// e o contador de abertos.
export function NoProjeto({ projeto, children }: { projeto: Projeto; children: ReactNode }) {
  const { id, name, role, is_default } = projeto
  const intervaloMinimo = intervaloMinimoDo(projeto)
  const atual = useMemo<ProjetoAtual>(
    () => ({ id, nome: name, papel: role, padrao: is_default, intervaloMinimo }),
    [id, name, role, is_default, intervaloMinimo],
  )
  return <Contexto.Provider value={atual}>{children}</Contexto.Provider>
}

export function useProjeto(): ProjetoAtual {
  const p = useContext(Contexto)
  if (!p) throw new Error('fora do ProvedorDoProjeto')
  return p
}

// Para quem também aparece fora de um projeto (a página não encontrada).
export function useProjetoSeHouver(): ProjetoAtual | null {
  return useContext(Contexto)
}

// O projeto da moldura (as abas e o contador), que fica acima das rotas do
// projeto. Numa tela de projeto, o da URL, se a pessoa for membro dele; fora
// da lista, nenhum, e nenhuma consulta de recurso sai para ele. Fora das
// telas de projeto (a conta), o que a raiz escolheria. Até a lista de
// projetos chegar, nenhum.
export function useProjetoDaMoldura(): Projeto | null {
  const naUrl = useMatch('/projects/:projectId/*')?.params.projectId
  const items = useProjetos().data?.items
  if (!items) return null
  if (naUrl !== undefined) return items.find((p) => p.id === naUrl) ?? null
  return escolherProjeto(items, lerProjetoLembrado())
}

export function useApiDoProjeto(): ApiDoProjeto {
  const api = useApi()
  const { id } = useProjeto()
  return useMemo(() => api.doProjeto(id), [api, id])
}

// O começo dos caminhos do projeto atual: '/projects/{id}'.
export function usePrefixoDoProjeto(): string {
  return `/projects/${encodeURIComponent(useProjeto().id)}`
}

// O caminho de uma tela dentro do projeto atual: '/targets/1' vira
// '/projects/{id}/targets/1'.
export function useRotaDoProjeto(): (rota: `/${string}`) => string {
  const prefixo = usePrefixoDoProjeto()
  return useCallback((rota) => `${prefixo}${rota}`, [prefixo])
}

// As seções de um projeto além do painel: trocar de projeto numa delas fica
// nela. O resto (o painel, o detalhe, o cadastro e a edição de um monitor, e
// as telas da conta) vai ao painel do outro projeto: o monitor de um projeto
// não existe no outro.
const SECOES = ['/incidents', '/channels', '/status-pages']

export function caminhoNoProjeto(caminhoAtual: string, id: string): string {
  const caminho = caminhoAtual.match(/^\/projects\/[^/]+(\/.*)?$/)?.[1] ?? ''
  const secao = SECOES.find((s) => caminho === s || caminho.startsWith(`${s}/`)) ?? '/'
  return `/projects/${encodeURIComponent(id)}${secao}`
}
