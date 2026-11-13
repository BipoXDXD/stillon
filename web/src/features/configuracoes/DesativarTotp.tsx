import { useState } from 'react'
import { useApi } from '@/app/contexto'
import { erroDeCredencial } from './credencial'
import type { ErroDeCredencial } from './credencial'
import { FormularioDeProvas } from './passosDoTotp'

export function DesativarTotp({ aoTerminar }: { aoTerminar: () => void }) {
  const api = useApi()
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  return (
    <FormularioDeProvas
      acao="Desativar"
      tipo="perigo"
      erro={erro}
      aoMudarErro={setErro}
      aoEnviar={async (senha, codigo) => {
        setErro(null)
        try {
          await api.totpDesativar(senha, codigo)
          aoTerminar()
        } catch (e) {
          setErro(erroDeCredencial(e, 'Senha ou código incorretos.'))
        }
      }}
    />
  )
}
