import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link, useNavigate } from 'react-router'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import {
  campoSenhaNova,
  CampoDeSenhaNova,
  marcarCamposDoServidor,
  MolduraPublica,
  TextoDeApoio,
  useEnvioDoFormulario,
  useTokenDoFragmento,
} from './comum'

const esquema = z.object({ senha: campoSenhaNova('Informe a senha nova.') })

// /reset-password#token=…: o token sai do fragmento como na confirmação, mas
// nada vai ao servidor na carga; ele só é conferido no envio.
export function PaginaRedefinirSenha() {
  const token = useTokenDoFragmento()
  const api = useApi()
  const navegar = useNavigate()
  const [invalido, setInvalido] = useState(token === null)
  const [erro, setErro] = useState<string | null>(null)
  const form = useForm({ resolver: zodResolver(esquema), defaultValues: { senha: '' } })
  const { enviando, enviar } = useEnvioDoFormulario(form, 'senha')

  if (invalido || token === null) {
    return (
      <MolduraPublica>
        <Titulo key="invalido" aba="Link inválido ou vencido">
          Link inválido ou vencido
        </Titulo>
        <TextoDeApoio>O link de redefinição vale 1 hora e só funciona uma vez. Peça outro.</TextoDeApoio>
        <Botao asChild className="w-full">
          <Link to="/forgot-password">Pedir outro link</Link>
        </Botao>
      </MolduraPublica>
    )
  }

  async function salvar(v: z.infer<typeof esquema>, t: string) {
    setErro(null)
    try {
      await api.redefinirSenha({ token: t, new_password: v.senha })
      navegar('/login', { replace: true, state: { motivo: 'senha-redefinida' } })
    } catch (e) {
      if (e instanceof ApiError && e.code === 'token_invalid') {
        setInvalido(true)
        return
      }
      const campo = marcarCamposDoServidor(e, form, { new_password: 'senha' })
      if (!campo) setErro(mensagemDe(e))
      return { campo }
    }
  }

  return (
    <MolduraPublica>
      <Titulo key="formulario" aba="Nova senha">
        Escolha uma senha nova
      </Titulo>
      <TextoDeApoio>Ao salvar, todas as sessões abertas da sua conta são encerradas.</TextoDeApoio>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <form noValidate onSubmit={enviar((v) => salvar(v, token))}>
        <fieldset disabled={enviando} className="min-w-0 space-y-4">
          <CampoDeSenhaNova rotulo="Senha nova" erro={form.formState.errors.senha?.message} registro={form.register('senha')} />
          <Botao type="submit" className="w-full">
            {enviando ? 'Salvando…' : 'Salvar senha'}
          </Botao>
        </fieldset>
      </form>
    </MolduraPublica>
  )
}
