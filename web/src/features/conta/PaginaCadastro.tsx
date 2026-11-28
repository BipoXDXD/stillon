import { zodResolver } from '@hookform/resolvers/zod'
import { useId, useState } from 'react'
import { useForm, type UseFormRegisterReturn } from 'react-hook-form'
import { Link } from 'react-router'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { CaixaDeMarcar, Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import versaoDosTermos from '@/features/legal/versao.json'
import { useConfigDoCadastro } from '@/lib/api/consultas'
import { MARQUE_O_ACEITE, mensagemDe, TERMOS_DESATUALIZADOS } from '@/lib/api/mensagens'
import {
  campoEmail,
  CarregandoConfig,
  campoSenhaNova,
  CampoDeSenhaNova,
  ConfiraSeuEmail,
  EsperaDaVerificacao,
  FalhaDaConfig,
  LinkTerciario,
  marcarCamposDoServidor,
  MolduraPublica,
  TextoDeApoio,
  useDesafio,
  useEnvioDoFormulario,
} from './comum'
import { Turnstile } from './Turnstile'

const esquema = z.object({
  email: campoEmail,
  senha: campoSenhaNova('Informe a senha.'),
  aceite: z.boolean().refine((v) => v, MARQUE_O_ACEITE),
})

const TITULO = 'Criar conta no StillOn'
const NOME_DO_ACEITE = 'Li e aceito os Termos de Uso e a Política de Privacidade'

export function PaginaCadastro() {
  const config = useConfigDoCadastro()
  const api = useApi()
  const desafio = useDesafio()
  const [erro, setErro] = useState<string | null>(null)
  const [enviadoPara, setEnviadoPara] = useState<string | null>(null)
  const form = useForm({ resolver: zodResolver(esquema), defaultValues: { email: '', senha: '', aceite: false } })
  const { enviando, enviar } = useEnvioDoFormulario(form, 'email')

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
      await api.cadastrar({
        email: v.email,
        password: v.senha,
        turnstile_token: desafio.token,
        accepted_terms_version: versaoDosTermos.versao,
      })
      setEnviadoPara(v.email)
    } catch (e) {
      // O formato é conferido antes do Turnstile: o token não foi gasto.
      const campo = marcarCamposDoServidor(e, form, {
        email: 'email',
        password: 'senha',
        accepted_terms_version: 'aceite',
      })
      if (!campo) {
        setErro(mensagemDe(e))
        desafio.recomecar()
      }
      return { campo }
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
      ) : config.data.terms_version !== versaoDosTermos.versao ? (
        // O painel carregado é de antes dos termos novos: o servidor recusaria
        // a versão que ele enviaria, e só recarregar traz o texto novo.
        <ErroDoFormulario>{TERMOS_DESATUALIZADOS}</ErroDoFormulario>
      ) : (
        <>
          <ErroDoFormulario>{erro}</ErroDoFormulario>
          <form noValidate onSubmit={enviar(cadastrar)}>
            <fieldset disabled={enviando} className="min-w-0 space-y-4">
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
              <CaixaDoAceite erro={form.formState.errors.aceite?.message} registro={form.register('aceite')} />
              <Turnstile
                siteKey={config.data.turnstile_site_key}
                acao="signup"
                tema={desafio.tema}
                aoMudar={desafio.aoMudar}
                falhou={desafio.falhou}
                reiniciar={desafio.reiniciar}
              />
              <Botao
                type="submit"
                className="w-full"
                disabled={!desafio.token}
                aria-describedby={enviando ? undefined : desafio.descritoPor}
              >
                {enviando ? 'Criando conta…' : 'Criar conta'}
              </Botao>
              <EsperaDaVerificacao visivel={desafio.aguardando && !enviando} />
            </fieldset>
          </form>
        </>
      )}
    </MolduraPublica>
  )
}

// Os textos abrem em aba nova: o formulário já preenchido fica onde está.
function LinkDoTexto({ para, children }: { para: string; children: string }) {
  return (
    <a href={para} target="_blank" rel="noopener" className="font-semibold text-primaria underline underline-offset-4">
      {children}
      <span className="sr-only"> (abre em nova aba)</span>
    </a>
  )
}

// A caixa do aceite, com o erro ligado a ela como o Campo faz com as entradas.
// O nome vem de um texto escondido sem os avisos de aba nova: tirado do
// rótulo, ele levaria o "(abre em nova aba)" dos dois links.
function CaixaDoAceite({ erro, registro }: { erro?: string; registro: UseFormRegisterReturn }) {
  const idErro = useId()
  const idNome = useId()
  return (
    <div className="space-y-2">
      <span id={idNome} hidden>
        {NOME_DO_ACEITE}
      </span>
      <CaixaDeMarcar
        aria-labelledby={idNome}
        aria-invalid={erro ? true : undefined}
        aria-describedby={erro ? idErro : undefined}
        {...registro}
      >
        <span>
          Li e aceito os <LinkDoTexto para="/termos">Termos de Uso</LinkDoTexto> e a{' '}
          <LinkDoTexto para="/privacidade">Política de Privacidade</LinkDoTexto>.
        </span>
      </CaixaDeMarcar>
      {erro && (
        <p id={idErro} className="text-sm text-perigo">
          {erro}
        </p>
      )}
    </div>
  )
}
