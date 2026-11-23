import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { useConfigDoCadastro } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import {
  campoEmail,
  CarregandoConfig,
  ConfiraSeuEmail,
  EsperaDaVerificacao,
  FalhaDaConfig,
  ID_DA_ESPERA,
  LinkTerciario,
  marcarCamposDoServidor,
  MolduraPublica,
  TextoDeApoio,
  useDesafio,
} from './comum'
import { Turnstile } from './Turnstile'

const esquema = z.object({ email: campoEmail })

const TITULO = 'Redefinir a senha'

// O link de redefinição sai só para e-mail com conta, mas a tela diz o mesmo
// nos dois casos: a resposta do servidor é a mesma (202), e o texto também.
export function PaginaEsqueciSenha() {
  const config = useConfigDoCadastro()
  const api = useApi()
  const desafio = useDesafio()
  const [erro, setErro] = useState<string | null>(null)
  const [enviadoPara, setEnviadoPara] = useState<string | null>(null)
  const form = useForm({ resolver: zodResolver(esquema), defaultValues: { email: '' } })
  const enviando = form.formState.isSubmitting

  // A redefinição depende do mesmo e-mail e do mesmo Turnstile do cadastro.
  if (config.data && !config.data.enabled) {
    return (
      <MolduraPublica>
        <Titulo key="fechado" aba={TITULO}>
          {TITULO}
        </Titulo>
        <TextoDeApoio>
          A redefinição por e-mail não está ativa neste servidor. Fale com quem administra o StillOn.
        </TextoDeApoio>
        <Botao asChild className="w-full">
          <Link to="/login">Voltar para entrar</Link>
        </Botao>
      </MolduraPublica>
    )
  }

  if (enviadoPara !== null) {
    return (
      <MolduraPublica>
        <ConfiraSeuEmail
          key="enviado"
          email={enviadoPara}
          frase="Abra o link em até 1 hora para escolher uma senha nova."
          aoPedirDeNovo={() => {
            setEnviadoPara(null)
            desafio.recomecar()
          }}
        />
      </MolduraPublica>
    )
  }

  async function pedir(v: z.infer<typeof esquema>) {
    if (!desafio.token) return
    setErro(null)
    try {
      await api.pedirRedefinicao({ email: v.email, turnstile_token: desafio.token })
      setEnviadoPara(v.email)
    } catch (e) {
      // O formato é conferido antes do Turnstile: o token não foi gasto.
      if (marcarCamposDoServidor(e, form, { email: 'email' })) return
      setErro(mensagemDe(e))
      desafio.recomecar()
    }
  }

  return (
    <MolduraPublica
      rodape={
        <>
          Lembrou a senha? <LinkTerciario para="/login">Entrar</LinkTerciario>
        </>
      }
    >
      <Titulo key="formulario" aba={TITULO}>
        {TITULO}
      </Titulo>
      {config.isPending ? (
        <CarregandoConfig />
      ) : config.isError ? (
        <FalhaDaConfig erro={config.error} aoTentar={() => void config.refetch()} />
      ) : (
        <>
          <TextoDeApoio>Informe o e-mail da sua conta. Mandamos um link para escolher uma senha nova.</TextoDeApoio>
          <ErroDoFormulario>{erro}</ErroDoFormulario>
          <form noValidate className="space-y-4" onSubmit={form.handleSubmit(pedir)}>
            <Campo rotulo="E-mail" erro={form.formState.errors.email?.message}>
              {(a) => (
                <Entrada
                  {...a}
                  type="text"
                  inputMode="email"
                  autoCapitalize="none"
                  spellCheck={false}
                  autoComplete="username"
                  {...form.register('email')}
                />
              )}
            </Campo>
            <Turnstile
              siteKey={config.data.turnstile_site_key}
              acao="forgot"
              tema={desafio.tema}
              aoResolver={desafio.aoResolver}
              reiniciar={desafio.reiniciar}
            />
            <Botao
              type="submit"
              className="w-full"
              disabled={!desafio.token || enviando}
              aria-describedby={!desafio.token && !enviando ? ID_DA_ESPERA : undefined}
            >
              {enviando ? 'Enviando…' : 'Enviar link'}
            </Botao>
            <EsperaDaVerificacao visivel={!desafio.token && !enviando} />
          </form>
        </>
      )}
    </MolduraPublica>
  )
}
