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
  campoSenhaNova,
  CampoDeSenhaNova,
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

const esquema = z.object({ email: campoEmail, senha: campoSenhaNova('Informe a senha.') })

const TITULO = 'Criar conta no StillOn'

export function PaginaCadastro() {
  const config = useConfigDoCadastro()
  const api = useApi()
  const desafio = useDesafio()
  const [erro, setErro] = useState<string | null>(null)
  const [enviadoPara, setEnviadoPara] = useState<string | null>(null)
  const form = useForm({ resolver: zodResolver(esquema), defaultValues: { email: '', senha: '' } })
  const enviando = form.formState.isSubmitting

  if (config.data && !config.data.enabled) {
    return (
      <MolduraPublica>
        <Titulo key="fechado" aba="Cadastro fechado">
          Cadastro fechado
        </Titulo>
        <TextoDeApoio>Este servidor não aceita contas novas no momento.</TextoDeApoio>
        <Botao asChild className="w-full">
          <Link to="/login">Entrar</Link>
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
          frase="Abra o link em até 24 horas para concluir o cadastro."
          aoPedirDeNovo={() => {
            setEnviadoPara(null)
            desafio.recomecar()
          }}
        />
      </MolduraPublica>
    )
  }

  async function cadastrar(v: z.infer<typeof esquema>) {
    if (!desafio.token) return
    setErro(null)
    try {
      await api.cadastrar({ email: v.email, password: v.senha, turnstile_token: desafio.token })
      setEnviadoPara(v.email)
    } catch (e) {
      // O formato é conferido antes do Turnstile: o token não foi gasto.
      if (marcarCamposDoServidor(e, form, { email: 'email', password: 'senha' })) return
      setErro(mensagemDe(e))
      desafio.recomecar()
    }
  }

  return (
    <MolduraPublica
      rodape={
        <>
          Já tem conta? <LinkTerciario para="/login">Entrar</LinkTerciario>
        </>
      }
    >
      <Titulo key="formulario" aba="Criar conta">
        {TITULO}
      </Titulo>
      {config.isPending ? (
        <CarregandoConfig />
      ) : config.isError ? (
        <FalhaDaConfig erro={config.error} aoTentar={() => void config.refetch()} />
      ) : (
        <>
          <ErroDoFormulario>{erro}</ErroDoFormulario>
          <form noValidate className="space-y-4" onSubmit={form.handleSubmit(cadastrar)}>
            <Campo rotulo="E-mail" erro={form.formState.errors.email?.message}>
              {(a) => (
                <Entrada
                  {...a}
                  type="text"
                  inputMode="email"
                  autoCapitalize="none"
                  spellCheck={false}
                  autoComplete="email"
                  {...form.register('email')}
                />
              )}
            </Campo>
            <CampoDeSenhaNova rotulo="Senha" erro={form.formState.errors.senha?.message} registro={form.register('senha')} />
            <Turnstile
              siteKey={config.data.turnstile_site_key}
              acao="signup"
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
              {enviando ? 'Criando conta…' : 'Criar conta'}
            </Botao>
            <EsperaDaVerificacao visivel={!desafio.token && !enviando} />
          </form>
        </>
      )}
    </MolduraPublica>
  )
}
