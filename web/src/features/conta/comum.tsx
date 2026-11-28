import { Eye, EyeOff } from 'lucide-react'
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode, type SubmitEventHandler } from 'react'
import type { FieldValues, Path, UseFormRegisterReturn, UseFormReturn } from 'react-hook-form'
import { Link, useLocation, useNavigate } from 'react-router'
import { z } from 'zod'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Marca } from '@/components/Marca'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { errosDosCampos } from '@/lib/api/erros-de-campo'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import { useTema } from '@/lib/tema'
import { cn } from '@/lib/utils'
import { ID_DA_FALHA_DO_TURNSTILE, temaDoTurnstile, type ResultadoDoTurnstile } from './Turnstile'

// O que as quatro telas públicas do cadastro dividem: a moldura do login, os
// campos, o "confira seu e-mail" e a leitura do token do fragmento.

// A mesma frase de tom do login (spec do redesign, Telas 5).
const FRASE = 'Seus serviços, vigiados de perto.'

// O cartão do login. rodape: o link de volta, abaixo do cartão. larga: para
// texto longo (os textos legais), com a mesma moldura.
export function MolduraPublica({
  children,
  rodape,
  larga = false,
}: {
  children: ReactNode
  rodape?: ReactNode
  larga?: boolean
}) {
  return (
    <>
      <AvisoDeContexto />
      <main className={cn('mx-auto space-y-8 px-4 py-12 sm:py-16', larga ? 'max-w-2xl' : 'max-w-md')}>
        <div className="space-y-2">
          <Marca tamanho="grande" />
          <p className="font-display text-xl text-suave">{FRASE}</p>
        </div>
        <div className="space-y-6 rounded-padrao border border-borda bg-superficie p-6 sombra-nivel-2 sm:p-8">
          {children}
        </div>
        {rodape && <p className="text-center text-sm text-suave">{rodape}</p>}
      </main>
    </>
  )
}

// O visual dos links destas telas, sem a área de toque.
export const ESTILO_DO_LINK = 'font-semibold text-primaria underline underline-offset-4'

// Um link terciário: o "Entrar" do rodapé e as ações que só navegam.
export function LinkTerciario({ para, children }: { para: string; children: ReactNode }) {
  return (
    <Link to={para} className={cn('alvo-de-toque', ESTILO_DO_LINK)}>
      {children}
    </Link>
  )
}

// O link no meio de um parágrafo: inline, sem o alvo-de-toque, cujo ::after
// de 44 px cobriria as linhas vizinhas do texto.
export function LinkEmProsa({ para, children }: { para: string; children: ReactNode }) {
  return (
    <Link to={para} className={ESTILO_DO_LINK}>
      {children}
    </Link>
  )
}

// Enquanto a config do cadastro não chega, o cartão mostra só o título.
export function CarregandoConfig() {
  return (
    <p aria-busy="true" className="text-sm text-suave">
      Carregando…
    </p>
  )
}

export function FalhaDaConfig({ erro, aoTentar }: { erro: unknown; aoTentar: () => void }) {
  return (
    <>
      <ErroDoFormulario>{mensagemDe(erro)}</ErroDoFormulario>
      <Botao tipo="secundario" className="w-full" onClick={aoTentar}>
        Tentar de novo
      </Botao>
    </>
  )
}

export function TextoDeApoio({ children }: { children: ReactNode }) {
  return <p className="text-sm text-suave">{children}</p>
}

// Os limites de auth.MinPasswordLen e MaxPasswordLen, contados em code points
// como o servidor: o cliente nunca é mais estrito que ele.
const MIN_SENHA = 15
const MAX_SENHA = 128
const letras = (s: string) => [...s].length

// O e-mail como no login: o "@" não recusa ninguém, e o resto é do servidor.
export const campoEmail = z
  .string()
  .trim()
  .min(1, 'Informe o e-mail.')
  .includes('@', { message: 'Informe um e-mail válido.' })

export function campoSenhaNova(vazia: string) {
  return z
    .string()
    .min(1, vazia)
    .refine((s) => s === '' || letras(s) >= MIN_SENHA, 'Mínimo de 15 caracteres.')
    .refine((s) => letras(s) <= MAX_SENHA, 'Máximo de 128 caracteres.')
}

export const AJUDA_DA_SENHA = 'De 15 a 128 caracteres.'

// Senha nova num campo só, com "Mostrar senha" (escolha 1 do dono). Colar e
// gerenciador de senha nunca são bloqueados (WCAG 3.3.8).
export function CampoDeSenhaNova({
  rotulo,
  erro,
  registro,
}: {
  rotulo: string
  erro?: string
  registro: UseFormRegisterReturn
}) {
  const [visivel, setVisivel] = useState(false)
  return (
    <Campo rotulo={rotulo} ajuda={AJUDA_DA_SENHA} erro={erro}>
      {(a) => (
        <div className="relative">
          <Entrada
            {...a}
            type={visivel ? 'text' : 'password'}
            autoComplete="new-password"
            autoCapitalize="none"
            spellCheck={false}
            className="pr-12"
            {...registro}
          />
          <Botao
            tipo="terciario"
            tamanho="icone"
            className="absolute top-0 right-0"
            aria-label="Mostrar senha"
            aria-pressed={visivel}
            onClick={() => setVisivel((v) => !v)}
          >
            {visivel ? <EyeOff aria-hidden="true" className="size-5" /> : <Eye aria-hidden="true" className="size-5" />}
          </Botao>
        </div>
      )}
    </Campo>
  )
}

// O primeiro erro de campo recebe foco depois que os controles são reabilitados.
export function useEnvioDoFormulario<T extends FieldValues>(form: UseFormReturn<T>, campoInicial: Path<T>) {
  type Falha = { campo?: Path<T> }
  type Estado = 'pronto' | 'enviando' | (Falha & { formulario: HTMLFormElement; origem: HTMLElement | null })
  const [estado, setEstado] = useState<Estado>('pronto')
  const emAndamento = useRef(false)
  const { handleSubmit, setFocus } = form

  useLayoutEffect(() => {
    if (typeof estado === 'string' || !estado.formulario.isConnected) return
    const { campo, origem } = estado
    const ativo = document.activeElement
    // Se a pessoa saiu do formulário durante o pedido, respeite o novo foco.
    if (ativo !== document.body && ativo !== origem) return
    if (campo) setFocus(campo)
    else if (origem && !origem.matches(':disabled')) origem.focus()
    else setFocus(campoInicial)
  }, [campoInicial, estado, setFocus])

  function enviar(acao: (dados: T) => Promise<Falha | void>): SubmitEventHandler<HTMLFormElement> {
    return (evento) => {
      evento.preventDefault()
      if (emAndamento.current) return
      emAndamento.current = true
      const formulario = evento.currentTarget
      const ativo = document.activeElement
      const origem = ativo instanceof HTMLElement && formulario.contains(ativo) ? ativo : null

      void handleSubmit(async (dados) => {
        setEstado('enviando')
        let falha: Falha | void = undefined
        try {
          falha = await acao(dados)
        } finally {
          setEstado(falha ? { ...falha, formulario, origem } : 'pronto')
        }
      })(evento).finally(() => {
        emAndamento.current = false
      })
    }
  }

  return { enviando: estado === 'enviando', enviar }
}

// Os erros de campo do 400 validation_failed. O mapa traduz o nome no servidor
// para o campo da tela, e o retorno identifica o primeiro campo recusado.
export function marcarCamposDoServidor<T extends FieldValues>(
  e: unknown,
  form: UseFormReturn<T>,
  mapa: Record<string, Path<T>>,
): Path<T> | undefined {
  const erros = errosDosCampos(e, (fe) => {
    const campo = mapa[fe.field]
    return campo ? { campo, mensagem: mensagemDoCampo(fe.field, fe.reason) } : undefined
  })
  erros.forEach(({ campo, mensagem }) => form.setError(campo, { message: mensagem }))
  return erros[0]?.campo
}

// O desafio do Turnstile de um formulário: o token (null até resolver) e o
// contador que reseta o widget depois de um envio recusado.
export function useDesafio() {
  const { tema } = useTema()
  const [resultado, aoMudar] = useState<ResultadoDoTurnstile>({ fase: 'aguardando' })
  const [reiniciar, setReiniciar] = useState(0)
  const recomecar = useCallback(() => {
    aoMudar({ fase: 'aguardando' })
    setReiniciar((n) => n + 1)
  }, [])
  const token = resultado.fase === 'resolvido' ? resultado.token : null
  const aguardando = resultado.fase === 'aguardando'
  const falhou = resultado.fase === 'falhou'
  const descritoPor = aguardando ? ID_DA_ESPERA : falhou ? ID_DA_FALHA_DO_TURNSTILE : undefined
  return { token, aguardando, falhou, descritoPor, reiniciar, aoMudar, recomecar, tema: temaDoTurnstile(tema) }
}

const ID_DA_ESPERA = 'espera-da-verificacao'

// Abaixo do botão, só enquanto ele espera o widget.
export function EsperaDaVerificacao({ visivel }: { visivel: boolean }) {
  if (!visivel) return null
  return (
    <p id={ID_DA_ESPERA} className="text-meta text-suave">
      Aguarde a verificação acima.
    </p>
  )
}

// "Confira seu e-mail", depois do signup e do forgot. O texto é o mesmo com e
// sem conta (anti-enumeração); só a frase do fluxo muda entre os dois.
export function ConfiraSeuEmail({
  email,
  frase,
  aoPedirDeNovo,
}: {
  email: string
  frase: string
  aoPedirDeNovo: () => void
}) {
  return (
    <>
      <Titulo aba="Confira seu e-mail">Confira seu e-mail</Titulo>
      <div className="space-y-3 text-sm">
        <p>
          Enviamos um link para <strong className="break-all">{email}</strong>, se ele puder receber mensagens do
          StillOn.
        </p>
        <p>{frase}</p>
        <p className="text-suave">Não chegou em alguns minutos? Confira a caixa de spam ou peça de novo.</p>
      </div>
      <div className="space-y-2">
        <Botao asChild tipo="secundario" className="w-full">
          <Link to="/login">Voltar para entrar</Link>
        </Botao>
        <Botao tipo="terciario" className="w-full" onClick={aoPedirDeNovo}>
          Pedir de novo
        </Botao>
      </div>
    </>
  )
}

// O token dos links de e-mail vem no fragmento (#token=…): o navegador não o
// manda ao servidor, e ele não aparece em log de acesso nem no Referer. A tela
// o guarda em memória e tira o fragmento da barra (history.replaceState, pelo
// router) antes de qualquer outra coisa.
export function useTokenDoFragmento(): string | null {
  const local = useLocation()
  const navegar = useNavigate()
  const [token] = useState(() => new URLSearchParams(local.hash.slice(1)).get('token') || null)
  useEffect(() => {
    if (local.hash) {
      void navegar({ pathname: local.pathname, search: local.search }, { replace: true, state: local.state })
    }
  }, [local, navegar])
  return token
}
