import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { useLayoutEffect, useRef, useState, type ComponentProps, type RefObject } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { useApi, useSessao } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { useCopiar } from '@/components/ui/copiar'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { Confirmar, Dialogo } from '@/components/ui/dialogo'
import { chaves, useMe, useSessoes } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import type { Sessao } from '@/lib/api/schemas'
import { dataHora, navegadorDe, pontosDeCodigo } from '@/lib/format'

// As rotas de credencial (/auth/password, /auth/totp/*) nunca renovam o
// token sozinhas (reconciliação, item 3): um "unauthorized" delas é o
// access token vencido nesta tela (ou, com a Task 11 do Plano 5, a família
// da sessão morta). A tela guarda o formulário e oferece renovar; renovado,
// quem envia de novo é o usuário — nada em /auth/* se repete sozinho.
// renovado: não é erro, é o "Acesso renovado. Envie de novo." — some no
// próximo envio, como qualquer aviso do formulário.
type ErroDeCredencial = { texto: string; renovavel: boolean; renovado?: boolean }

// Um código que não é de 6 dígitos é de recuperação (16 caracteres base32).
const ehDeRecuperacao = (codigo: string | undefined) => !!codigo && !/^\d{6}$/.test(codigo)

function erroDeCredencial(e: unknown, errado: string, codigoEnviado?: string): ErroDeCredencial {
  let r: ErroDeCredencial
  if (e instanceof ApiError && e.code === 'invalid_credentials') r = { texto: errado, renovavel: false }
  else if (e instanceof ApiError && e.code === 'unauthorized') {
    r = { texto: 'O acesso desta tela venceu.', renovavel: true }
  } else r = { texto: mensagemDe(e), renovavel: false }
  // PENDENCIAS do Plano 6: o código de recuperação é consumido ANTES da
  // transação da troca de senha. Se ela falha depois (família revogada,
  // outra troca concorrente), o código já foi gasto.
  // Sem resposta (a rede, o prazo), o servidor pode ter gasto o código do
  // mesmo jeito: o rotate que estourou o prazo pode ter comitado.
  if (e instanceof ApiError && (e.status === 401 || respostaPerdida(e)) && ehDeRecuperacao(codigoEnviado)) {
    r = { ...r, texto: `${r.texto} Se você usou um código de recuperação, ele pode ter sido consumido; use outro.` }
  }
  return r
}

// O erro de um formulário de credencial, com "Renovar acesso e tentar de
// novo" quando o que venceu foi o acesso. Refresh recusado é o fim da
// sessão (volta ao login com o motivo), como em qualquer outra rota.
function AvisoDeCredencial({
  erro,
  aoMudar,
}: {
  erro: ErroDeCredencial | null
  aoMudar: (e: ErroDeCredencial | null) => void
}) {
  const { sessao } = useSessao()
  const [falha, setFalha] = useState<string | null>(null)
  // A falha de renovar é do erro em que o botão foi clicado: quando o erro
  // muda (outro envio, ou nenhum), ela sai, para não esconder o texto novo.
  const [erroDaFalha, setErroDaFalha] = useState(erro)
  if (erro !== erroDaFalha) {
    setErroDaFalha(erro)
    setFalha(null)
  }
  if (!erro) return null
  if (erro.renovado) return <p className="text-sm">{erro.texto}</p>
  return (
    <div className="space-y-2">
      <ErroDoFormulario>{falha ?? erro.texto}</ErroDoFormulario>
      {erro.renovavel && (
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={async () => {
            setFalha(null)
            try {
              const novo = await sessao.renovar(sessao.token())
              if (!novo) {
                sessao.encerrar('expirou')
                return
              }
              aoMudar({ texto: 'Acesso renovado. Envie de novo.', renovavel: false, renovado: true })
            } catch (e) {
              setFalha(mensagemDe(e))
            }
          }}
        >
          Renovar acesso e tentar de novo
        </Botao>
      )}
    </div>
  )
}

// Política de senha nova do Plano 5 (D1): 15 a 128 code points, qualquer
// caractere, sem regra de composição. O servidor é a fonte da verdade.
const novaSenha = z
  .string()
  .refine((s) => pontosDeCodigo(s) >= 15, 'Mínimo de 15 caracteres.')
  .refine((s) => pontosDeCodigo(s) <= 128, 'Máximo de 128 caracteres.')

function TrocaDeSenha({ comTotp }: { comTotp: boolean }) {
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const esquema = z
    .object({ atual: z.string().min(1, 'Obrigatório.'), nova: novaSenha, confirmacao: z.string(), codigo: z.string() })
    .refine((v) => v.nova === v.confirmacao, { path: ['confirmacao'], message: 'As duas senhas não são iguais.' })
    .refine((v) => !comTotp || v.codigo.trim().length >= 6, { path: ['codigo'], message: 'Informe o código.' })
  const f = useForm({
    resolver: zodResolver(esquema),
    defaultValues: { atual: '', nova: '', confirmacao: '', codigo: '' },
  })
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  const e = f.formState.errors

  return (
    <form
      noValidate
      className="max-w-md space-y-4"
      onSubmit={f.handleSubmit(async (v) => {
        setErro(null)
        const codigo = comTotp ? v.codigo.replace(/\s+/g, '') : undefined
        try {
          await api.trocarSenha({
            current_password: v.atual,
            new_password: v.nova,
            ...(codigo !== undefined ? { totp_code: codigo } : {}),
          })
          f.reset()
          await qc.invalidateQueries({ queryKey: chaves.sessoes })
          avisar(
            'Senha alterada. As outras sessões foram encerradas; um acesso já aberto em outro aparelho pode durar até 15 minutos.',
          )
        } catch (err) {
          if (err instanceof ApiError && err.fields[0]?.field === 'new_password') {
            f.setError(
              'nova',
              { message: mensagemDoCampo('new_password', err.fields[0].reason) },
              { shouldFocus: true },
            )
          } else {
            // Não há como separar senha errada de código errado, e não se deve.
            setErro(
              erroDeCredencial(err, comTotp ? 'Senha atual ou código incorretos.' : 'Senha atual incorreta.', codigo),
            )
          }
        }
      })}
    >
      <AvisoDeCredencial erro={erro} aoMudar={setErro} />
      <Campo rotulo="Senha atual" erro={e.atual?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...f.register('atual')} />}
      </Campo>
      <Campo rotulo="Senha nova" erro={e.nova?.message} ajuda="De 15 a 128 caracteres. Uma frase serve.">
        {(a) => <Entrada {...a} type="password" autoComplete="new-password" {...f.register('nova')} />}
      </Campo>
      <Campo rotulo="Confirme a senha nova" erro={e.confirmacao?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="new-password" {...f.register('confirmacao')} />}
      </Campo>
      {comTotp && (
        <Campo rotulo="Código do autenticador" erro={e.codigo?.message} ajuda="Ou um código de recuperação.">
          {(a) => <Entrada {...a} autoComplete="one-time-code" {...f.register('codigo')} />}
        </Campo>
      )}
      <Botao type="submit" disabled={f.formState.isSubmitting}>
        Trocar a senha
      </Botao>
    </form>
  )
}

const JA_ATIVADA = 'A verificação em duas etapas já está ativada.'
const JA_DESATIVADA = 'A verificação em duas etapas já está desativada.'
// Fechar no passo 2 da troca (spec do Plano 9 §4.2). O pendente fica no
// servidor sem efeito: só um confirm com o código do app novo o usa.
const NADA_MUDOU = 'Nada mudou: o app atual continua valendo.'

// Quanto a URL do .txt dos códigos vive depois do clique (o do FileSaver.js).
export const ATRASO_DA_REVOGACAO = 40_000

// O confirm responde o mesmo 400 para código errado e para cadastro que
// sumiu (trocar a senha apaga o cadastro pendente: PENDENCIAS do Plano 6).
const CODIGO_NAO_ACEITO =
  'Código incorreto. Se a senha foi trocada depois de o QR code aparecer, feche e comece de novo.'

function segredoDe(uri: string): string {
  try {
    return new URL(uri).searchParams.get('secret') ?? ''
  } catch {
    return ''
  }
}

// Um envio vazio iria ao servidor e contaria como tentativa falha no
// bloqueio progressivo (R34). Só "não vazio": o formato do código (6
// dígitos ou os 16 de recuperação) quem decide é o servidor.
const esquemaSenha = z.object({ senha: z.string().min(1, 'Informe a senha.') })
const esquemaCodigo = z.object({ codigo: z.string().trim().min(1, 'Informe o código.') })

// 403 no enroll ou no confirm: o 2FA já está ligado (outra aba o ligou com
// este diálogo aberto; totphandlers.go).
const jaAtivada = (e: unknown) => e instanceof ApiError && e.status === 403

// A resposta de um confirm se perdeu (E2 do Plano 10): sem rede, prazo
// estourado (os dois chegam como 'network'), um 5xx do proxy sem o envelope,
// ou um 2xx que não se lê. O servidor pode ter gravado. Um erro COM o
// envelope é a resposta do servidor, e ela diz o que aconteceu.
const respostaPerdida = (e: unknown) =>
  e instanceof ApiError && (e.code === 'network' || (e.code === 'unexpected' && (e.status >= 500 || e.status < 400)))

const ATIVACAO_INCERTA =
  'A resposta do servidor não chegou, e não dá para saber se a verificação em duas etapas foi ativada. Feche e confira nesta tela: se ela estiver ativada, os códigos de recuperação desta ativação se perderam; use "Trocar autenticador" para gerar outros.'
const TROCA_INCERTA =
  'A resposta do servidor não chegou, e não dá para saber se a troca foi concluída. Saia e entre com um código do app novo: se ele valer, a troca foi feita e os códigos de recuperação antigos não valem mais (troque o autenticador de novo para gerar outros); se não valer, o app atual continua valendo.'

// Um envio por vez (revisão final do Plano 9). O handleSubmit do
// react-hook-form não impede reentrada: um clique duplo (ou Enter duas vezes)
// mandava dois rotate, ou dois confirm — e o 2º confirm, com o pendente já
// consumido pelo 1º, voltava 403 e tirava da tela os códigos novos. A ref
// segura a reentrada no mesmo tique; o estado alimenta o aria-disabled.
function useEnvioUnico() {
  const emVoo = useRef(false)
  const [enviando, setEnviando] = useState(false)
  const enviar = async (fn: () => Promise<void>) => {
    if (emVoo.current) return
    emVoo.current = true
    setEnviando(true)
    try {
      await fn()
    } finally {
      emVoo.current = false
      setEnviando(false)
    }
  }
  return { enviando, enviar }
}

// O QR e a chave de um segredo novo, e o código que o app gera com ele: o
// passo 2 de ativar e o de trocar de autenticador (spec do Plano 9 §4.2).
function PassoDoQr({
  uri,
  instrucao,
  acao,
  erro,
  aoMudarErro,
  aoEnviar,
}: {
  uri: string
  instrucao: string
  acao: string
  erro: ErroDeCredencial | null
  aoMudarErro: (e: ErroDeCredencial | null) => void
  aoEnviar: (codigo: string) => Promise<void>
}) {
  const codigo = useForm({ resolver: zodResolver(esquemaCodigo), defaultValues: { codigo: '' } })
  const { enviando, enviar } = useEnvioUnico()
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={codigo.handleSubmit((v) => enviar(() => aoEnviar(v.codigo.replace(/\s+/g, ''))))}
    >
      <AvisoDeCredencial erro={erro} aoMudar={aoMudarErro} />
      <p className="text-sm">{instrucao}</p>
      <div className="inline-block bg-white p-3">
        <QRCodeSVG value={uri} size={176} title="QR code do autenticador" />
      </div>
      <p className="text-sm">
        Chave: <code className="break-all">{segredoDe(uri)}</code>
      </p>
      <Campo rotulo="Código de 6 dígitos" erro={codigo.formState.errors.codigo?.message}>
        {(a) => <Entrada {...a} inputMode="numeric" autoComplete="one-time-code" {...codigo.register('codigo')} />}
      </Campo>
      {/* aria-disabled, e não disabled: o botão desabilitado perderia o foco,
          que cairia no <body> dentro do diálogo. Quem segura o envio é o
          useEnvioUnico. */}
      <Botao type="submit" aria-disabled={enviando || undefined}>
        {acao}
      </Botao>
    </form>
  )
}

// Os códigos de recuperação novos, que só existem nesta tela: copiar,
// baixar, e "Concluir" só depois de "Guardei os códigos". O último passo de
// ativar e o de trocar de autenticador.
function CodigosDeRecuperacao({ codigos, aoTerminar }: { codigos: string[]; aoTerminar: () => void }) {
  const [guardei, setGuardei] = useState(false)
  const { copiado, copiar } = useCopiar({
    ok: 'Códigos copiados.',
    falha: 'Não foi possível copiar. Selecione os códigos e copie à mão, ou baixe o .txt.',
  })
  return (
    <div className="space-y-4">
      <p className="text-sm">
        Guarde estes códigos de recuperação num lugar seguro. Cada um vale uma vez, e eles não aparecem de novo.
      </p>
      <ul className="grid grid-cols-2 gap-1 font-mono text-sm">
        {codigos.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Botao tipo="secundario" tamanho="pequeno" onClick={() => copiar(codigos.join('\n'))}>
          {copiado ? 'Copiado' : 'Copiar'}
        </Botao>
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={() => {
            const url = URL.createObjectURL(new Blob([codigos.join('\n') + '\n'], { type: 'text/plain' }))
            const a = document.createElement('a')
            a.href = url
            a.download = 'vigilume-codigos-de-recuperacao.txt'
            a.click()
            // O Firefox cancela o download se a URL é revogada logo depois
            // do click(); este é o único arquivo dos códigos.
            window.setTimeout(() => URL.revokeObjectURL(url), ATRASO_DA_REVOGACAO)
          }}
        >
          Baixar .txt
        </Botao>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={guardei} onChange={(e) => setGuardei(e.target.checked)} />
        Guardei os códigos
      </label>
      <Botao disabled={!guardei} onClick={aoTerminar}>
        Concluir
      </Botao>
    </div>
  )
}

// Senha e posse do fator atual (código do app ou de recuperação): as
// provas do Desativar e as do passo 1 da troca de autenticador, que são as
// mesmas no servidor (spec do Plano 9 §3.2).
function FormularioDeProvas({
  acao,
  tipo,
  erro,
  aoMudarErro,
  aoEnviar,
}: {
  acao: string
  tipo: 'perigo' | 'primario'
  erro: ErroDeCredencial | null
  aoMudarErro: (e: ErroDeCredencial | null) => void
  aoEnviar: (senha: string, codigo: string) => Promise<void>
}) {
  const f = useForm({ resolver: zodResolver(esquemaSenha.and(esquemaCodigo)), defaultValues: { senha: '', codigo: '' } })
  const { enviando, enviar } = useEnvioUnico()
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={f.handleSubmit((v) => enviar(() => aoEnviar(v.senha, v.codigo.replace(/\s+/g, ''))))}
    >
      <AvisoDeCredencial erro={erro} aoMudar={aoMudarErro} />
      <Campo rotulo="Sua senha" erro={f.formState.errors.senha?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...f.register('senha')} />}
      </Campo>
      <Campo rotulo="Código do autenticador" ajuda="Ou um código de recuperação." erro={f.formState.errors.codigo?.message}>
        {(a) => <Entrada {...a} autoComplete="one-time-code" {...f.register('codigo')} />}
      </Campo>
      <Botao type="submit" tipo={tipo} aria-disabled={enviando || undefined}>
        {acao}
      </Botao>
    </form>
  )
}

function AtivarTotp({
  aoTerminar,
  aoOcupar,
  aoTravar,
  aoJaAtivada,
  aoIncerta,
}: {
  aoTerminar: () => void
  // O confirm está em voo: o diálogo não fecha até a resposta.
  aoOcupar: (ocupado: boolean) => void
  aoTravar: () => void
  aoJaAtivada: () => void
  // A resposta do confirm se perdeu: fechar avisa com este texto.
  aoIncerta: (texto: string) => void
}) {
  const api = useApi()
  const [passo, setPasso] = useState<'senha' | 'codigo' | 'codigos'>('senha')
  const [uri, setUri] = useState('')
  const [codigos, setCodigos] = useState<string[]>([])
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  const senha = useForm({ resolver: zodResolver(esquemaSenha), defaultValues: { senha: '' } })
  // Um enroll por vez: dois gravariam dois segredos, e o QR na tela seria o da
  // última resposta a chegar, não necessariamente o que ficou no servidor.
  const { enviando, enviar } = useEnvioUnico()

  if (passo === 'senha') {
    return (
      <form
        noValidate
        className="space-y-4"
        onSubmit={senha.handleSubmit((v) =>
          enviar(async () => {
            setErro(null)
            try {
              setUri((await api.totpCadastrar(v.senha)).otpauth_uri)
              setPasso('codigo')
            } catch (e) {
              if (jaAtivada(e)) {
                setErro({ texto: JA_ATIVADA, renovavel: false })
                aoJaAtivada()
              } else setErro(erroDeCredencial(e, 'Senha incorreta.'))
            }
          }),
        )}
      >
        <AvisoDeCredencial erro={erro} aoMudar={setErro} />
        <Campo rotulo="Sua senha" erro={senha.formState.errors.senha?.message}>
          {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...senha.register('senha')} />}
        </Campo>
        <Botao type="submit" aria-disabled={enviando || undefined}>
          Continuar
        </Botao>
      </form>
    )
  }
  if (passo === 'codigo') {
    return (
      <PassoDoQr
        uri={uri}
        instrucao="Leia o QR code com o aplicativo autenticador, ou digite a chave."
        acao="Ativar"
        erro={erro}
        aoMudarErro={setErro}
        aoEnviar={async (c) => {
          setErro(null)
          // Travado ANTES de aguardar (revisão final do Plano 9): fechar com
          // o confirm em voo dizia "nada mudou" e perdia os códigos, se ele
          // desse certo; e o aoTravar, rodando depois, deixava o travado
          // preso para o diálogo seguinte.
          aoOcupar(true)
          try {
            setCodigos((await api.totpConfirmar(c)).recovery_codes)
            // Daqui em diante os códigos só existem nesta tela: Esc e o
            // clique fora não fecham o diálogo, só "Concluir".
            aoTravar()
            setPasso('codigos')
          } catch (e) {
            if (respostaPerdida(e)) {
              setErro({ texto: ATIVACAO_INCERTA, renovavel: false })
              aoIncerta(ATIVACAO_INCERTA)
              return
            }
            if (jaAtivada(e)) {
              setErro({ texto: JA_ATIVADA, renovavel: false })
              aoJaAtivada()
              return
            }
            setErro(
              e instanceof ApiError && e.code === 'validation_failed'
                ? { texto: CODIGO_NAO_ACEITO, renovavel: false }
                : erroDeCredencial(e, 'Código incorreto.'),
            )
          } finally {
            aoOcupar(false)
          }
        }}
      />
    )
  }
  return <CodigosDeRecuperacao codigos={codigos} aoTerminar={aoTerminar} />
}

function DesativarTotp({ aoTerminar }: { aoTerminar: () => void }) {
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

// 403 no rotate/confirm: não há troca pendente. Outra aba concluiu a troca,
// ou a cancelou (desativar apaga o pendente; outro rotate o substitui).
const TROCA_SUMIU = 'A troca foi concluída ou cancelada em outra aba. Comece de novo.'

// A troca de autenticador (spec do Plano 9 §4.2), em três passos: as provas
// do Desativar; o QR do app NOVO e o código dele; os 10 códigos novos. O
// app atual vale até o passo 2 ser confirmado.
function TrocarTotp({
  aoIniciar,
  aoVoltar,
  aoOcupar,
  aoTravar,
  aoTerminar,
  aoDesativada,
  aoIncerta,
}: {
  // O passo 2 começou: fechar agora não muda nada (e, com um código de
  // recuperação no passo 1, ele já foi gasto: R1 da spec).
  aoIniciar: (usouRecuperacao: boolean) => void
  aoVoltar: () => void
  // O rotate ou o confirm está em voo: o diálogo não fecha até a resposta.
  aoOcupar: (ocupado: boolean) => void
  aoTravar: () => void
  aoTerminar: () => void
  aoDesativada: () => void
  // A resposta do confirm se perdeu: fechar avisa com este texto.
  aoIncerta: (texto: string) => void
}) {
  const api = useApi()
  const qc = useQueryClient()
  const [passo, setPasso] = useState<'provas' | 'codigo' | 'codigos'>('provas')
  // O QR do app novo e o rotation do pendente dele (A7 do Plano 10): o
  // confirm manda o rotation DESTE QR, e um pendente que outra aba
  // substituiu responde 403.
  const [troca, setTroca] = useState({ otpauth_uri: '', rotation: '' })
  const [codigos, setCodigos] = useState<string[]>([])
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  // Os códigos novos já estão na tela: uma resposta atrasada de outro
  // confirm (um 403, o pendente já consumido) não pode tirá-los de lá.
  const concluida = useRef(false)
  // Um confirm anterior perdeu a resposta: um 403 agora pode ser ele mesmo
  // (a troca feita), e não "outra aba".
  const incerta = useRef(false)

  if (passo === 'provas') {
    return (
      <FormularioDeProvas
        acao="Continuar"
        tipo="primario"
        erro={erro}
        aoMudarErro={setErro}
        aoEnviar={async (senha, codigo) => {
          setErro(null)
          // Travado antes de aguardar, como os confirms (E1 do Plano 10):
          // fechar com o rotate em voo deixava a resposta sem diálogo, e o
          // código de recuperação, se era um, gasto sem aviso.
          aoOcupar(true)
          try {
            setTroca(await api.totpTrocar(senha, codigo))
            aoIniciar(ehDeRecuperacao(codigo))
            setPasso('codigo')
          } catch (e) {
            // 403: o 2FA foi desligado em outra aba; não há o que trocar.
            if (e instanceof ApiError && e.status === 403) aoDesativada()
            // O código vai junto: um 401 depois de um código de recuperação
            // avisa que ele pode ter sido gasto (E5), como na troca de senha.
            else setErro(erroDeCredencial(e, 'Senha ou código incorretos.', codigo))
          } finally {
            aoOcupar(false)
          }
        }}
      />
    )
  }
  if (passo === 'codigo') {
    return (
      <PassoDoQr
        uri={troca.otpauth_uri}
        instrucao="Leia o QR code com o aplicativo autenticador novo, ou digite a chave. O app atual continua valendo até você confirmar."
        acao="Confirmar a troca"
        erro={erro}
        aoMudarErro={setErro}
        aoEnviar={async (c) => {
          setErro(null)
          // Travado antes de aguardar, como na ativação.
          aoOcupar(true)
          try {
            setCodigos((await api.totpConfirmarTroca(c, troca.rotation)).recovery_codes)
            // Os códigos só existem nesta tela, como na ativação.
            concluida.current = true
            aoTravar()
            setPasso('codigos')
            // O confirm encerrou as outras sessões.
            void qc.invalidateQueries({ queryKey: chaves.sessoes })
          } catch (e) {
            if (concluida.current) return
            if (respostaPerdida(e) || (incerta.current && e instanceof ApiError && e.status === 403)) {
              incerta.current = true
              setErro({ texto: TROCA_INCERTA, renovavel: false })
              aoIncerta(TROCA_INCERTA)
              return
            }
            if (e instanceof ApiError && e.status === 403) {
              // O QR desta tela não vale mais: de volta ao passo 1.
              setErro({ texto: TROCA_SUMIU, renovavel: false })
              aoVoltar()
              setPasso('provas')
            } else setErro(erroDeCredencial(e, 'Código incorreto.'))
          } finally {
            aoOcupar(false)
          }
        }}
      />
    )
  }
  return <CodigosDeRecuperacao codigos={codigos} aoTerminar={aoTerminar} />
}

function Sessoes() {
  const consulta = useSessoes()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const { sessao } = useSessao()
  const [alvo, setAlvo] = useState<Sessao | null>(null)
  const [erro, setErro] = useState<string | null>(null)
  // Um DELETE por vez: um segundo DELETE da sessão atual voltaria 401 (a
  // família já morreu) e trocaria o motivo do fim da sessão para "expirou".
  const [ocupado, setOcupado] = useState(false)

  async function encerrar() {
    if (!alvo || ocupado) return
    const atual = alvo.current
    setErro(null)
    setOcupado(true)
    try {
      await api.encerrarSessao(alvo.id)
      if (atual) {
        // O 204 já apagou o cookie (D15 do Plano 5). O access token em
        // memória ainda valeria até 15 minutos: o painel é quem o larga,
        // sem chamar refresh nem logout, e sem recarregar nada.
        sessao.encerrar('encerrada-aqui')
        return
      }
      avisar('Sessão encerrada.')
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_found') avisar('Esta sessão já tinha terminado.')
      else setErro(mensagemDe(e))
    }
    // A lista muda ANTES de o diálogo fechar: fechando antes, o foco voltaria
    // ao "Encerrar" da linha, que sumiria em seguida (e o foco, ao <body>).
    // Com a linha já fora, o diálogo manda o foco ao título da tela.
    await qc.invalidateQueries({ queryKey: chaves.sessoes })
    setAlvo(null)
    setOcupado(false)
  }

  return (
    <>
      <p className="text-sm text-suave">
        Encerrar impede a renovação. Um acesso já aberto no outro aparelho pode continuar por até 15 minutos.
      </p>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <EstadoDaConsulta consulta={consulta}>
        {(d) => (
          <ul className="space-y-2">
            {d.items.map((s) => (
              <li
                key={s.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-borda p-3"
              >
                <div className="text-sm">
                  <p className="font-medium">
                    {navegadorDe(s.user_agent)}
                    {s.current && <span className="ml-2 rounded bg-painel px-2 py-0.5 text-xs">Esta sessão</span>}
                  </p>
                  <p className="text-suave">
                    Desde {dataHora(s.created_at)} · último acesso por volta de {dataHora(s.last_used_at)}
                  </p>
                </div>
                <Botao tipo="perigo" tamanho="pequeno" onClick={() => setAlvo(s)}>
                  Encerrar
                </Botao>
              </li>
            ))}
          </ul>
        )}
      </EstadoDaConsulta>
      <Confirmar
        aberto={alvo !== null}
        aoMudar={(v) => !v && setAlvo(null)}
        titulo={alvo?.current ? 'Sair deste navegador?' : 'Encerrar a sessão?'}
        descricao={
          alvo?.current
            ? 'Esta é a sessão que você está usando: o painel volta para a tela de entrada.'
            : 'O outro aparelho não consegue mais renovar o acesso.'
        }
        acao="Encerrar"
        ocupado={ocupado}
        aoConfirmar={() => void encerrar()}
      />
    </>
  )
}

type Dialogo2fa = 'ativar' | 'desativar' | 'trocar'

// Um botão que some quando o /me muda (E6 do Plano 10): o "Trocar
// autenticador" clicado com o 2FA já desligado em outra aba some no /me
// relido, e o foco, que estava nele, ia para o <body>. Se ele tinha o foco,
// o foco vai à reserva. O efeito de layout roda na desmontagem ANTES de o
// React tirar o <button> do DOM, com a ref ainda presa a ele.
function BotaoQueSome({ reserva, ...props }: ComponentProps<typeof Botao> & { reserva: RefObject<HTMLElement | null> }) {
  const eu = useRef<HTMLButtonElement>(null)
  useLayoutEffect(() => {
    const botao = eu.current
    return () => {
      // A reserva é lida na desmontagem de propósito: é o botão que está na
      // tela nesse instante (o mesmo nó, que passou de Desativar a Ativar).
      // oxlint-disable-next-line react-hooks/exhaustive-deps
      if (botao && document.activeElement === botao) reserva.current?.focus()
    }
  }, [reserva])
  return <Botao ref={eu} {...props} />
}

export function PaginaConfiguracoes() {
  const me = useMe()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const [dialogo, setDialogo] = useState<Dialogo2fa | null>(null)
  const [travado, setTravado] = useState(false)
  // Um confirm (ativar ou trocar) em voo: o diálogo não fecha até a resposta,
  // como no passo dos códigos (revisão final do Plano 9).
  const [ocupado, setOcupado] = useState(false)
  const preso = travado || ocupado
  // A troca de autenticador está no passo 2 (o QR do app novo na tela):
  // fechar avisa que nada mudou. usouRecuperacao: o código de recuperação
  // do passo 1 já foi gasto (R1 da spec do Plano 9).
  const [trocaNoMeio, setTrocaNoMeio] = useState<{ usouRecuperacao: boolean } | null>(null)
  // A resposta de um confirm se perdeu (E2 do Plano 10): fechar avisa com
  // este texto, e nunca com "Nada mudou".
  const [incerta, setIncerta] = useState<string | null>(null)
  const [abrindo, setAbrindo] = useState(false)
  // O botão que fica na seção do 2FA (Ativar ou Desativar): a reserva do
  // foco quando o "Trocar autenticador" some.
  const botao2fa = useRef<HTMLButtonElement>(null)
  // ['me'] não vence sozinho (staleTime Infinity): outra aba pode ter
  // ligado ou desligado o 2FA. Abrir e fechar um diálogo do 2FA relê /me.
  const reler = () => void qc.invalidateQueries({ queryKey: chaves.me })
  // Abrir ESPERA o /me relido: decidir pelo cache abriria "Desativar" com o
  // 2FA já desligado em outra aba, e o disable contaria tentativa falha (R34).
  // "Trocar autenticador" tem a mesma trava: o rotate também conta.
  const abrir = async (d: Dialogo2fa) => {
    if (abrindo) return
    setAbrindo(true)
    let ligado: boolean
    try {
      ligado = (await qc.fetchQuery({ queryKey: chaves.me, queryFn: api.me, staleTime: 0 })).totp_enabled
    } catch (e) {
      avisar(mensagemDe(e), 'erro')
      return
    } finally {
      setAbrindo(false)
    }
    if (d === 'ativar' && ligado) avisar(JA_ATIVADA)
    else if (d !== 'ativar' && !ligado) avisar(JA_DESATIVADA)
    else {
      // Um diálogo novo começa solto, mesmo que um callback atrasado do
      // anterior tenha travado depois de ele fechar.
      setTravado(false)
      setOcupado(false)
      setTrocaNoMeio(null)
      setIncerta(null)
      setDialogo(d)
    }
  }
  const fechar = () => {
    if (incerta) avisar(incerta)
    else if (dialogo === 'trocar' && trocaNoMeio) {
      avisar(
        trocaNoMeio.usouRecuperacao
          ? `${NADA_MUDOU} O código de recuperação usado foi gasto; use outro da próxima vez.`
          : NADA_MUDOU,
      )
    }
    setDialogo(null)
    setTravado(false)
    setOcupado(false)
    setTrocaNoMeio(null)
    setIncerta(null)
    reler()
  }
  const terminar = (texto: string) => {
    fechar()
    avisar(texto)
  }
  const totp = me.data?.totp_enabled
  // O portão de cada diálogo: Ativar só com o 2FA desligado (ou no passo
  // dos códigos, travado); Desativar só com ele ligado; Trocar também só com
  // ele ligado (ou no passo dos códigos novos). Se o /me relido o fecha com
  // o diálogo aberto, o diálogo fecha de vez: sem isto, ele reapareceria
  // sozinho quando o /me mudasse de novo.
  const portao =
    dialogo === 'ativar'
      ? totp === false || preso
      : dialogo === 'desativar'
        ? totp === true
        : dialogo === 'trocar' && (totp === true || preso)
  if (dialogo !== null && !portao) {
    setDialogo(null)
    setTrocaNoMeio(null)
  }
  return (
    <>
      <Titulo aba="Configurações">Configurações</Titulo>
      <EstadoDaConsulta consulta={me}>
        {(eu) => (
          <>
            <section aria-labelledby="t-conta" className="space-y-1">
              <h2 id="t-conta" className="text-lg font-semibold">
                Conta
              </h2>
              <p>{eu.email}</p>
              <p className="text-sm text-suave">Desde {dataHora(eu.created_at)}</p>
            </section>
            <section aria-labelledby="t-senha" className="space-y-3">
              <h2 id="t-senha" className="text-lg font-semibold">
                Senha
              </h2>
              <TrocaDeSenha comTotp={eu.totp_enabled} />
            </section>
            <section aria-labelledby="t-2fa" className="space-y-3">
              <h2 id="t-2fa" className="text-lg font-semibold">
                Verificação em duas etapas
              </h2>
              <p className="text-sm">{eu.totp_enabled ? 'Ativada.' : 'Desativada.'}</p>
              <div className="flex flex-wrap gap-2">
                <Botao
                  ref={botao2fa}
                  tipo={eu.totp_enabled ? 'perigo' : 'primario'}
                  // aria-disabled, e não disabled: o botão desabilitado perde o
                  // foco, que cairia no <body>.
                  aria-disabled={abrindo || undefined}
                  onClick={() => void abrir(eu.totp_enabled ? 'desativar' : 'ativar')}
                >
                  {eu.totp_enabled ? 'Desativar' : 'Ativar'}
                </Botao>
                {eu.totp_enabled && (
                  <BotaoQueSome
                    reserva={botao2fa}
                    tipo="secundario"
                    aria-disabled={abrindo || undefined}
                    onClick={() => void abrir('trocar')}
                  >
                    Trocar autenticador
                  </BotaoQueSome>
                )}
              </div>
            </section>
          </>
        )}
      </EstadoDaConsulta>
      <section aria-labelledby="t-sessoes" className="space-y-3">
        <h2 id="t-sessoes" className="text-lg font-semibold">
          Sessões ativas
        </h2>
        <Sessoes />
      </section>
      {/* Ativar só com o 2FA desligado segundo o /me relido (outra aba pode
          tê-lo ligado: o enroll responderia 403). Depois do confirm, o /me
          diz "ligado", e o diálogo continua aberto pelo travado: os códigos
          de recuperação só existem nele. */}
      <Dialogo
        aberto={dialogo === 'ativar' && portao}
        aoMudar={(v) => !v && fechar()}
        travado={preso}
        titulo="Ativar a verificação em duas etapas"
      >
        <AtivarTotp
          aoOcupar={setOcupado}
          aoIncerta={setIncerta}
          aoTravar={() => {
            setTravado(true)
            setIncerta(null)
          }}
          aoTerminar={() => terminar('Verificação em duas etapas ativada.')}
          // O /me relido fecha o diálogo (o portão); o aviso fica no toast,
          // que sobrevive a ele.
          aoJaAtivada={() => {
            avisar(JA_ATIVADA)
            reler()
          }}
        />
      </Dialogo>
      {/* Desativar só com o 2FA ligado segundo o /me relido: sem 2FA, o
          POST /auth/totp/disable conta tentativa falha na conta (R34). */}
      <Dialogo
        aberto={dialogo === 'desativar' && portao}
        aoMudar={(v) => !v && fechar()}
        titulo="Desativar a verificação em duas etapas"
      >
        <DesativarTotp aoTerminar={() => terminar('Verificação em duas etapas desativada.')} />
      </Dialogo>
      {/* Trocar só com o 2FA ligado segundo o /me relido (o rotate conta
          tentativa falha, R34). No passo dos códigos, travado. */}
      <Dialogo
        aberto={dialogo === 'trocar' && portao}
        aoMudar={(v) => !v && fechar()}
        travado={preso}
        titulo="Trocar o autenticador"
      >
        <TrocarTotp
          aoIniciar={(usouRecuperacao) => setTrocaNoMeio({ usouRecuperacao })}
          aoVoltar={() => setTrocaNoMeio(null)}
          aoOcupar={setOcupado}
          aoIncerta={setIncerta}
          aoTravar={() => {
            setTravado(true)
            setTrocaNoMeio(null)
            setIncerta(null)
          }}
          aoTerminar={() => terminar('Autenticador trocado. As outras sessões foram encerradas.')}
          // O 2FA foi desligado em outra aba: fecha e avisa (o toast
          // sobrevive ao diálogo).
          aoDesativada={() => {
            fechar()
            avisar(JA_DESATIVADA)
          }}
        />
      </Dialogo>
    </>
  )
}
