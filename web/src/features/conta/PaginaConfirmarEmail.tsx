import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { chaves } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import { MolduraPublica, TextoDeApoio, useEnvioDoFormulario, useTokenDoFragmento } from './comum'

const esquema = z.object({ senha: z.string().min(1, 'Informe a senha do cadastro.') })

// /confirm-email#token=…: o token sai do fragmento, mas nada vai ao servidor
// na carga. A confirmação exige a senha escolhida no cadastro: quem abre o
// link em outro aparelho, ou o dono de um e-mail que alguém usou, não
// habilita a conta sem ela. A senha errada não gasta o link.
export function PaginaConfirmarEmail() {
  const token = useTokenDoFragmento()
  const api = useApi()
  const cliente = useQueryClient()
  const [invalido, setInvalido] = useState(token === null)
  const [confirmado, setConfirmado] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const form = useForm({ resolver: zodResolver(esquema), defaultValues: { senha: '' } })
  const { enviando, enviar } = useEnvioDoFormulario(form, 'senha')

  if (invalido || token === null) {
    return (
      <MolduraPublica>
        <Titulo key="invalido" aba="Link inválido ou vencido">
          Link inválido ou vencido
        </Titulo>
        <TextoDeApoio>
          O link de confirmação vale 24 horas e só funciona uma vez. Entre no StillOn e peça outro pelo aviso no topo do
          painel.
        </TextoDeApoio>
        <Botao asChild className="w-full">
          <Link to="/login">Entrar</Link>
        </Botao>
      </MolduraPublica>
    )
  }

  if (confirmado) {
    return (
      <MolduraPublica>
        <Titulo key="confirmado" aba="E-mail confirmado">
          E-mail confirmado
        </Titulo>
        <TextoDeApoio>Sua conta está pronta para criar monitores.</TextoDeApoio>
        {/* Sem sessão, o guarda do painel leva ao login e volta para cá. */}
        <Botao asChild className="w-full">
          <Link to="/">Ir para o painel</Link>
        </Botao>
      </MolduraPublica>
    )
  }

  async function confirmar(v: z.infer<typeof esquema>, t: string) {
    setErro(null)
    try {
      await api.confirmarEmail(t, v.senha)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'token_invalid') {
        setInvalido(true)
        return
      }
      if (e instanceof ApiError && e.code === 'invalid_credentials') {
        form.setError('senha', { message: 'Senha incorreta. O link continua valendo: tente de novo.' })
        return { campo: 'senha' as const }
      }
      setErro(mensagemDe(e))
      return {}
    }
    // O /me tem staleTime Infinity: sem isto, o painel aberto seguiria com o
    // aviso de e-mail não confirmado.
    void cliente.invalidateQueries({ queryKey: chaves.me })
    setConfirmado(true)
  }

  return (
    <MolduraPublica>
      <Titulo key="formulario" aba="Confirmar e-mail">
        Confirme seu e-mail
      </Titulo>
      <TextoDeApoio>
        Digite a senha que você escolheu no cadastro. Ela garante que só quem criou a conta consegue ativá-la.
      </TextoDeApoio>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <form noValidate onSubmit={enviar((v) => confirmar(v, token))}>
        <fieldset disabled={enviando} className="min-w-0 space-y-4">
          <Campo rotulo="Senha" erro={form.formState.errors.senha?.message}>
            {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...form.register('senha')} />}
          </Campo>
          <Botao type="submit" className="w-full">
            {enviando ? 'Confirmando…' : 'Confirmar e-mail'}
          </Botao>
        </fieldset>
      </form>
    </MolduraPublica>
  )
}
