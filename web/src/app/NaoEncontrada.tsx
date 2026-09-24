import { Link } from 'react-router'
import { Titulo } from '@/components/Titulo'

export function NaoEncontrada() {
  return (
    <div className="space-y-4">
      <Titulo aba="Página não encontrada">Página não encontrada</Titulo>
      <Link to="/" className="text-realce underline">
        Voltar ao painel
      </Link>
    </div>
  )
}
