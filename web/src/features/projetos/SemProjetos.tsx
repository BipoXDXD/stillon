import { useState } from 'react'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { DialogoNovoProjeto } from './DialogoNovoProjeto'

// A raiz de quem não participa de nenhum projeto (nunca teve, ou foi tirado
// do único que via). "Nenhum projeto", e não "o primeiro": a pessoa pode já
// ter tido algum. Nada aqui consulta recurso; o foco volta ao botão quando o
// diálogo fecha sem criar.
export function SemProjetos() {
  const [criando, setCriando] = useState(false)
  return (
    <div className="space-y-4">
      <Titulo aba="Nenhum projeto">Nenhum projeto</Titulo>
      <p className="max-w-prose text-suave">Alvos, canais e status pages ficam dentro de um projeto. Crie um para continuar.</p>
      <Botao onClick={() => setCriando(true)}>Criar projeto</Botao>
      <DialogoNovoProjeto aberto={criando} aoFechar={() => setCriando(false)} />
    </div>
  )
}
