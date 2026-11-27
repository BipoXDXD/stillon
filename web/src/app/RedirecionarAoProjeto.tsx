import { Navigate, useLocation, useNavigation } from 'react-router'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { SemProjetos } from '@/features/projetos/SemProjetos'
import { useProjetos } from '@/lib/api/consultas'
import { escolherProjeto, lerProjetoLembrado } from './escolher-projeto'

// O mesmo caminho, a mesma search e o mesmo hash, dentro do projeto. replace:
// o endereço antigo não fica no histórico, e o Voltar não cai nele de novo.
// Com outra navegação em espera (a pessoa clicou numa rota sob demanda antes
// de a lista chegar), não redireciona: o <Navigate> do react-router navega
// num efeito, sem conferir a localização, e cancelaria a navegação dela.
// Quando ela termina, esta rota já desmontou.
function NoProjeto({ id }: { id: string }) {
  const { pathname, search, hash } = useLocation()
  const emEspera = useNavigation().state !== 'idle'
  if (emEspera) return null
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
        // Sem projeto nenhum, não há para onde ir: o estado vazio, que cria.
        const projeto = escolherProjeto(items, lembrado())
        return projeto ? <NoProjeto id={projeto.id} /> : <SemProjetos />
      }}
    </EstadoDaConsulta>
  )
}
