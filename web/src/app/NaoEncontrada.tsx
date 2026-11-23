import { Link } from 'react-router'
import { Titulo } from '@/components/Titulo'
import { useProjetoSeHouver } from './projeto'

// Dentro de um projeto, a volta é ao painel dele; fora (a conta, ou um
// projeto que não é da pessoa), à raiz, que escolhe o projeto.
export function NaoEncontrada() {
  const projeto = useProjetoSeHouver()
  const painel = projeto ? `/projects/${encodeURIComponent(projeto.id)}/` : '/'
  return (
    <div className="space-y-4">
      <Titulo aba="Página não encontrada" semProjeto>
        Página não encontrada
      </Titulo>
      <Link to={painel} className="alvo-de-toque text-realce underline">
        Voltar ao painel
      </Link>
    </div>
  )
}
