import { Link } from 'react-router'
import { Titulo } from '@/components/Titulo'
import { usePrefixoDoProjeto } from './projeto'

// Quem chega pela URL a uma tela que o papel não permite (cadastrar ou editar
// um monitor sem edit_monitors): o formulário nem monta, porque o envio só
// daria 403. Texto provisório até a revisão dos textos da equipe (T12).
export function SemPermissao() {
  const prefixo = usePrefixoDoProjeto()
  return (
    <div className="space-y-4">
      <Titulo aba="Sem permissão">Sem permissão</Titulo>
      <p>Seu papel neste projeto não permite esta ação.</p>
      <Link to={`${prefixo}/`} className="alvo-de-toque text-realce underline">
        Voltar ao painel
      </Link>
    </div>
  )
}
