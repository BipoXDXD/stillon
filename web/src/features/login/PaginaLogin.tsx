import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Navigate, useLocation, useNavigate, useSearchParams } from 'react-router'
import { z } from 'zod'
import { useSessao } from '@/app/contexto'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import type { Motivo } from '@/lib/api/sessao'

// Só caminho do painel. "//evil.example" e "/\evil" são URLs de outro host
// para o navegador; sem esta regra, o ?next= seria um open redirect. Duas
// regras: caractere de controle ou "\" em qualquer lugar recusa (o parser de
// URL descarta tab e quebra de linha, e "/\t/evil" viraria "//evil"); e o
// destino, resolvido contra a origem do painel, tem de continuar nela.
export function destinoSeguro(next: string | null): string {
  // Casar caractere de controle é o propósito desta regex.
  // oxlint-disable-next-line no-control-regex
  if (!next || !next.startsWith('/') || /[\u0000-\u001f\u007f\\]/.test(next)) return '/'
  try {
    if (new URL(next, location.origin).origin !== location.origin) return '/'
  } catch {
    return '/'
  }
  return next
}

const avisosDeMotivo: Partial<Record<Motivo, string>> = {
  expirou: 'Sua sessão terminou. Entre de novo.',
  'outra-aba': 'Você saiu em outra aba.',
  'encerrada-aqui': 'Você encerrou esta sessão.',
}

const ID_MOTIVO = 'motivo-do-fim-da-sessao'

const esquemaSenha = z.object({
  // O cliente nunca é mais estrito que o servidor. O login do servidor só
  // apara os espaços (auth.NormalizeEmail), e o cadastro aceita o que o
  // mail.ParseAddress aceita: domínio internacional e parte local com
  // acento. Um z.email() recusava esses na tela, e a conta nunca entrava.
  // O "@" não recusa ninguém: todo e-mail cadastrado tem um.
  email: z.string().trim().min(1, 'Informe o e-mail.').includes('@', { message: 'Informe um e-mail válido.' }),
  // Sem a política de tamanho: ela vale só para senha nova, e o login aceita
  // a senha antiga mais curta (R5 do Plano 5).
  senha: z.string().min(1, 'Informe a senha.'),
})

const esquemaCodigo = z.object({ codigo: z.string().trim().min(6, 'Informe o código.') })

export function PaginaLogin() {
  const { sessao, estado } = useSessao()
  const [params] = useSearchParams()
  const local = useLocation()
  const navegar = useNavigate()
  const [desafio, setDesafio] = useState<string | null>(null)
  const [recuperacao, setRecuperacao] = useState(false)
  const [erro, setErro] = useState<string | null>(null)
  const destino = destinoSeguro(params.get('next'))
  const motivo = (local.state as { motivo?: Motivo } | null)?.motivo
  const textoDoMotivo = motivo ? avisosDeMotivo[motivo] : undefined

  const senha = useForm({ resolver: zodResolver(esquemaSenha), defaultValues: { email: '', senha: '' } })
  const codigo = useForm({ resolver: zodResolver(esquemaCodigo), defaultValues: { codigo: '' } })

  // Ao voltar do passo do código ao da senha (Voltar, ou o desafio vencido),
  // o botão que tinha o foco some com o formulário; sem isto o foco cairia no
  // <body>. O formulário da senha remonta (key), e o autoFocus do campo da
  // senha o leva para lá. Na carga da página, não: o primeiro Tab é o "Pular
  // para o conteúdo" (D20).
  const [voltou, setVoltou] = useState(false)

  // De volta ao passo da senha: o desafio fica para trás, e a senha é
  // digitada de novo.
  function voltarASenha(mensagem: string | null) {
    setDesafio(null)
    setRecuperacao(false)
    setErro(mensagem)
    senha.setValue('senha', '')
    codigo.setValue('codigo', '')
    setVoltou(true)
  }

  if (estado === 'autenticado') return <Navigate to={destino} replace />

  async function entrar(v: z.infer<typeof esquemaSenha>) {
    setErro(null)
    try {
      const r = await sessao.entrar(v.email, v.senha)
      if (r.tipo === 'totp') {
        setDesafio(r.desafio)
        return
      }
      navegar(destino, { replace: true })
    } catch (e) {
      // O mesmo texto para senha errada, e-mail inexistente e conta
      // bloqueada: o bloqueio não se anuncia (R34 do Plano 1).
      setErro(mensagemDe(e))
      senha.setValue('senha', '')
    }
  }

  async function verificar(v: z.infer<typeof esquemaCodigo>) {
    if (!desafio) return
    setErro(null)
    try {
      await sessao.verificarTotp(desafio, v.codigo.replace(/\s+/g, ''))
      navegar(destino, { replace: true })
    } catch (e) {
      if (e instanceof ApiError && e.code === 'unauthorized') {
        // O desafio vive 5 minutos (auth.ChallengeTTL).
        voltarASenha('O tempo para digitar o código acabou. Entre de novo.')
        return
      }
      // Task 11 do Plano 5: se a senha mudou depois deste desafio, o verify
      // responde invalid_credentials como para um código errado — e um
      // código de recuperação pode já ter sido gasto nele.
      setErro(
        e instanceof ApiError && e.code === 'invalid_credentials'
          ? 'Código incorreto. Se a senha mudou há pouco, volte e entre de novo.'
          : mensagemDe(e),
      )
      codigo.setValue('codigo', '')
    }
  }

  return (
    <>
      <AvisoDeContexto />
      <main className="mx-auto mt-16 max-w-sm space-y-6 px-4">
        {/* O foco vai ao <h1> depois de a sessão acabar (é uma navegação), e
            o leitor de tela lê o motivo junto, pela descrição. */}
        <Titulo aba="Entrar" descritoPor={textoDoMotivo ? ID_MOTIVO : undefined}>
          Entrar no StillOn
        </Titulo>
        {textoDoMotivo && (
          <p id={ID_MOTIVO} className="rounded-md border border-borda p-3 text-sm">
            {textoDoMotivo}
          </p>
        )}
        <ErroDoFormulario>{erro}</ErroDoFormulario>
        {desafio === null ? (
          // key nos dois formulários: trocar de passo monta campos novos, em vez
          // de o React reaproveitar o <input> do e-mail para o código.
          <form key="senha" noValidate className="space-y-4" onSubmit={senha.handleSubmit(entrar)}>
            {/* type="text" com inputMode="email": o <input type="email"> do
                Chromium entrega o domínio internacional em punycode no value,
                e o texto que chegaria ao servidor não seria o cadastrado. */}
            <Campo rotulo="E-mail" erro={senha.formState.errors.email?.message}>
              {(a) => (
                <Entrada
                  {...a}
                  type="text"
                  inputMode="email"
                  autoCapitalize="none"
                  spellCheck={false}
                  autoComplete="username"
                  {...senha.register('email')}
                />
              )}
            </Campo>
            <Campo rotulo="Senha" erro={senha.formState.errors.senha?.message}>
              {(a) => (
                <Entrada
                  {...a}
                  type="password"
                  autoComplete="current-password"
                  autoFocus={voltou}
                  {...senha.register('senha')}
                />
              )}
            </Campo>
            <Botao type="submit" className="w-full" disabled={senha.formState.isSubmitting}>
              Entrar
            </Botao>
          </form>
        ) : (
          <form key="codigo" noValidate className="space-y-4" onSubmit={codigo.handleSubmit(verificar)}>
            <Campo
              rotulo={recuperacao ? 'Código de recuperação' : 'Código do aplicativo autenticador'}
              ajuda={
                recuperacao ? '16 caracteres, cada código vale uma vez.' : 'Os 6 dígitos que o aplicativo mostra agora.'
              }
              erro={codigo.formState.errors.codigo?.message}
            >
              {(a) => (
                <Entrada
                  {...a}
                  autoFocus
                  autoComplete="one-time-code"
                  inputMode={recuperacao ? 'text' : 'numeric'}
                  {...codigo.register('codigo')}
                />
              )}
            </Campo>
            <Botao type="submit" className="w-full" disabled={codigo.formState.isSubmitting}>
              Verificar
            </Botao>
            <Botao tipo="fantasma" className="w-full" onClick={() => setRecuperacao((r) => !r)}>
              {recuperacao ? 'Usar o código do aplicativo' : 'Usar um código de recuperação'}
            </Botao>
            {/* A mensagem de invalid_credentials manda voltar; o Voltar do
                navegador sairia do /login, porque o passo é estado da tela. */}
            <Botao tipo="fantasma" className="w-full" onClick={() => voltarASenha(null)}>
              Voltar
            </Botao>
          </form>
        )}
      </main>
    </>
  )
}
