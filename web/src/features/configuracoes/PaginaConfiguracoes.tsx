import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { useApi, useSessao } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
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
  if (e instanceof ApiError && e.status === 401 && ehDeRecuperacao(codigoEnviado)) {
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

function AtivarTotp({ aoTerminar, aoTravar }: { aoTerminar: () => void; aoTravar: () => void }) {
  const api = useApi()
  const [passo, setPasso] = useState<'senha' | 'codigo' | 'codigos'>('senha')
  const [uri, setUri] = useState('')
  const [codigos, setCodigos] = useState<string[]>([])
  const [guardei, setGuardei] = useState(false)
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  const senha = useForm({ defaultValues: { senha: '' } })
  const codigo = useForm({ defaultValues: { codigo: '' } })

  if (passo === 'senha') {
    return (
      <form
        noValidate
        className="space-y-4"
        onSubmit={senha.handleSubmit(async (v) => {
          setErro(null)
          try {
            setUri((await api.totpCadastrar(v.senha)).otpauth_uri)
            setPasso('codigo')
          } catch (e) {
            setErro(erroDeCredencial(e, 'Senha incorreta.'))
          }
        })}
      >
        <AvisoDeCredencial erro={erro} aoMudar={setErro} />
        <Campo rotulo="Sua senha">
          {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...senha.register('senha')} />}
        </Campo>
        <Botao type="submit">Continuar</Botao>
      </form>
    )
  }
  if (passo === 'codigo') {
    return (
      <form
        noValidate
        className="space-y-4"
        onSubmit={codigo.handleSubmit(async (v) => {
          setErro(null)
          try {
            setCodigos((await api.totpConfirmar(v.codigo.replace(/\s+/g, ''))).recovery_codes)
            // Daqui em diante os códigos só existem nesta tela: Esc e o
            // clique fora não fecham o diálogo, só "Concluir".
            aoTravar()
            setPasso('codigos')
          } catch (e) {
            setErro(
              e instanceof ApiError && e.code === 'validation_failed'
                ? { texto: CODIGO_NAO_ACEITO, renovavel: false }
                : erroDeCredencial(e, 'Código incorreto.'),
            )
          }
        })}
      >
        <AvisoDeCredencial erro={erro} aoMudar={setErro} />
        <p className="text-sm">Leia o QR code com o aplicativo autenticador, ou digite a chave.</p>
        <div className="inline-block bg-white p-3">
          <QRCodeSVG value={uri} size={176} title="QR code do autenticador" />
        </div>
        <p className="text-sm">
          Chave: <code className="break-all">{segredoDe(uri)}</code>
        </p>
        <Campo rotulo="Código de 6 dígitos">
          {(a) => <Entrada {...a} inputMode="numeric" autoComplete="one-time-code" {...codigo.register('codigo')} />}
        </Campo>
        <Botao type="submit">Ativar</Botao>
      </form>
    )
  }
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
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={() => void navigator.clipboard?.writeText(codigos.join('\n'))}
        >
          Copiar
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
            URL.revokeObjectURL(url)
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

function DesativarTotp({ aoTerminar }: { aoTerminar: () => void }) {
  const api = useApi()
  const f = useForm({ defaultValues: { senha: '', codigo: '' } })
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={f.handleSubmit(async (v) => {
        setErro(null)
        try {
          await api.totpDesativar(v.senha, v.codigo.replace(/\s+/g, ''))
          aoTerminar()
        } catch (e) {
          setErro(erroDeCredencial(e, 'Senha ou código incorretos.'))
        }
      })}
    >
      <AvisoDeCredencial erro={erro} aoMudar={setErro} />
      <Campo rotulo="Sua senha">
        {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...f.register('senha')} />}
      </Campo>
      <Campo rotulo="Código do autenticador" ajuda="Ou um código de recuperação.">
        {(a) => <Entrada {...a} autoComplete="one-time-code" {...f.register('codigo')} />}
      </Campo>
      <Botao type="submit" tipo="perigo">
        Desativar
      </Botao>
    </form>
  )
}

function Sessoes() {
  const consulta = useSessoes()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const { sessao } = useSessao()
  const [alvo, setAlvo] = useState<Sessao | null>(null)
  const [erro, setErro] = useState<string | null>(null)

  async function encerrar() {
    if (!alvo) return
    const atual = alvo.current
    setErro(null)
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
    setAlvo(null)
    await qc.invalidateQueries({ queryKey: chaves.sessoes })
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
        aoConfirmar={() => void encerrar()}
      />
    </>
  )
}

export function PaginaConfiguracoes() {
  const me = useMe()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const [dialogo, setDialogo] = useState<'ativar' | 'desativar' | null>(null)
  const [travado, setTravado] = useState(false)
  const [abrindo, setAbrindo] = useState(false)
  // ['me'] não vence sozinho (staleTime Infinity): outra aba pode ter
  // ligado ou desligado o 2FA. Abrir e fechar um diálogo do 2FA relê /me.
  const reler = () => void qc.invalidateQueries({ queryKey: chaves.me })
  // Abrir ESPERA o /me relido: decidir pelo cache abriria "Desativar" com o
  // 2FA já desligado em outra aba, e o disable contaria tentativa falha (R34).
  const abrir = async (d: 'ativar' | 'desativar') => {
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
    else if (d === 'desativar' && !ligado) avisar('A verificação em duas etapas já está desativada.')
    else setDialogo(d)
  }
  const fechar = () => {
    setDialogo(null)
    setTravado(false)
    reler()
  }
  const terminar = (texto: string) => {
    fechar()
    avisar(texto)
  }
  const totp = me.data?.totp_enabled
  // O portão de cada diálogo: Ativar só com o 2FA desligado (ou no passo
  // dos códigos, travado); Desativar só com ele ligado. Se o /me relido o
  // fecha com o diálogo aberto, o diálogo fecha de vez: sem isto, ele
  // reapareceria sozinho quando o /me mudasse de novo.
  const portao = dialogo === 'ativar' ? totp === false || travado : dialogo === 'desativar' && totp === true
  if (dialogo !== null && !portao) setDialogo(null)
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
              <Botao
                tipo={eu.totp_enabled ? 'perigo' : 'primario'}
                // aria-disabled, e não disabled: o botão desabilitado perde o
                // foco, que cairia no <body>.
                aria-disabled={abrindo || undefined}
                onClick={() => void abrir(eu.totp_enabled ? 'desativar' : 'ativar')}
              >
                {eu.totp_enabled ? 'Desativar' : 'Ativar'}
              </Botao>
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
        travado={travado}
        titulo="Ativar a verificação em duas etapas"
      >
        <AtivarTotp
          aoTravar={() => setTravado(true)}
          aoTerminar={() => terminar('Verificação em duas etapas ativada.')}
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
    </>
  )
}
