import { Navigate, useLocation } from 'react-router'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { useProjetos } from '@/lib/api/consultas'
import { escolherProjeto, lerProjetoLembrado } from './escolher-projeto'
import { NaoEncontrada } from './NaoEncontrada'

// O mesmo caminho, a mesma search e o mesmo hash, dentro do projeto. replace:
// o endereço antigo não fica no histórico, e o Voltar não cai nele de novo.
function NoProjeto({ id }: { id: string }) {
  const { pathname, search, hash } = useLocation()
  return <Navigate replace to={`/projects/${encodeURIComponent(id)}${pathname}${search}${hash}`} />
}

// A raiz: o último projeto aberto neste navegador, se a pessoa ainda for
// membro dele; senão o padrão.
export function RedirecionarAoEscolhido() {
  return <Redirecionar lembrado={lerProjetoLembrado} />
}

// Os links de antes dos projetos (/targets/…, /incidents…): sempre o Projeto
// padrão, onde os recursos de antes ficaram.
export function RedirecionarAoProjeto() {
  return <Redirecionar lembrado={() => null} />
}

function Redirecionar({ lembrado }: { lembrado: () => string | null }) {
  const projetos = useProjetos()
  return (
    <EstadoDaConsulta consulta={projetos}>
      {({ items }) => {
        const projeto = escolherProjeto(items, lembrado())
        return projeto ? <NoProjeto id={projeto.id} /> : <NaoEncontrada />
      }}
    </EstadoDaConsulta>
  )
}
