import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { Bell, CircleCheck, CircleX, Clock, Pause, TimerOff, type LucideIcon } from 'lucide-react'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Selo, type TomDoSelo } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao, BotaoQueSome, TITULO_DA_TELA } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario, Opcao } from '@/components/ui/campo'
import { estiloDeCartao } from '@/components/ui/cartao'
import { Confirmar, Dialogo } from '@/components/ui/dialogo'
import { chaves, useCanais } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo, textoDoErroDeCanal } from '@/lib/api/mensagens'
import type { Canal } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'
import { assertUnreachable, cn } from '@/lib/utils'

// 'desconhecido' é um tipo que uma versão posterior do servidor criou: a linha
// aparece, sem as ações que só fazem sentido para um tipo conhecido.
function rotuloDoTipo(kind: Canal['kind']): string {
  switch (kind) {
    case 'discord':
      return 'Discord'
    case 'email':
      return 'E-mail'
    case 'desconhecido':
      return 'Outro tipo'
    default:
      return assertUnreachable(kind)
  }
}

// O nome do canal nas linhas, nos botões, no diálogo e nos avisos. Sem hint (o
// servidor não sabe ler o destino de um tipo que não conhece, ou o destino
// ficou ilegível), a data de criação distingue as linhas: com dois canais
// assim, o dono precisa saber qual está apagando.
function nomeDoCanal(c: Canal): string {
  return c.hint.trim() === '' ? `criado em ${dataHora(c.created_at)}` : c.hint
}

// Estado da verificação com forma, cor e texto (auditoria C2, S9).
const estados: Record<Canal['status'], { texto: string; Icone: LucideIcon; tom: TomDoSelo }> = {
  verified: { texto: 'Verificado', Icone: CircleCheck, tom: 'no-ar' },
  pending: { texto: 'Aguardando verificação', Icone: Clock, tom: 'degradado' },
  expired: { texto: 'Verificação expirada', Icone: TimerOff, tom: 'sem-dados' },
  failing: { texto: 'Falhando', Icone: CircleX, tom: 'fora' },
}

// Canal pausado (auditoria C3): o que importa é que os avisos são
// descartados, então "Pausado" é o selo, em âmbar (o alvo pausado do painel
// é cinza: pausar um alvo não perde nada). A verificação continua, rebaixada
// a texto apagado.
function EstadoDoCanal({ canal }: { canal: Canal }) {
  const { texto, Icone, tom } = estados[canal.status]
  if (canal.enabled) {
    return (
      <Selo tom={tom} Icone={Icone}>
        {texto}
      </Selo>
    )
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Selo tom="degradado" Icone={Pause}>
        Pausado
      </Selo>
      <span className="text-sm">Os avisos são descartados.</span>
      <span className="inline-flex items-center gap-1 text-meta text-suave">
        <Icone aria-hidden="true" className="size-3.5 shrink-0" />
        {texto}
      </span>
    </div>
  )
}

const esquema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('discord'), url: z.string().trim().pipe(z.url('Cole a URL do webhook.')) }),
  // Só o mínimo: quem decide se o e-mail vale é o servidor (net/mail com um
  // ponto no domínio), que aceita o que z.email() recusa: acentos, IDN,
  // "a!b@…". Um 400 email/invalid volta como texto.
  z.object({
    kind: z.literal('email'),
    email: z.string().trim().min(1, 'Informe o e-mail.').refine((s) => s.includes('@'), 'Informe um e-mail válido.'),
  }),
])
type Valores = z.input<typeof esquema>

// Os erros de criar e de reenviar vêm do mesmo balde do servidor (1 envio a
// cada 6 minutos) e do mesmo vocabulário; muda o que falhou ao entregar.
function textoDoErro(e: unknown, acao: 'criar' | 'reenviar'): string {
  if (e instanceof ApiError && e.code === 'validation_failed' && e.fields[0]) {
    const { field, reason } = e.fields[0]
    // Aqui a URL é a de um webhook, não a de um alvo.
    if (field === 'url' && reason === 'invalid') {
      return 'Use a URL do webhook do Discord (https://discord.com/api/webhooks/…).'
    }
    return mensagemDoCampo(field, reason)
  }
  if (e instanceof ApiError && e.code === 'rate_limited') {
    return 'Limite de envios atingido (1 a cada 6 minutos): tente de novo em alguns minutos.'
  }
  if (e instanceof ApiError && e.code === 'delivery_failed' && acao === 'reenviar') {
    return 'Não conseguimos entregar o e-mail de verificação. Tente de novo mais tarde.'
  }
  return mensagemDe(e)
}

function NovoCanal({ aoCriar }: { aoCriar: (c: Canal) => void }) {
  const api = useApi()
  const f = useForm<Valores>({
    resolver: zodResolver(esquema),
    defaultValues: { kind: 'discord', url: '' },
  })
  const [erro, setErro] = useState<string | null>(null)
  const tipo = useWatch({ control: f.control, name: 'kind' })
  const erros = f.formState.errors as Partial<Record<'url' | 'email', { message?: string }>>

  async function enviar(v: Valores) {
    setErro(null)
    try {
      const c = await api.criarCanal(
        v.kind === 'discord' ? { kind: 'discord', url: v.url } : { kind: 'email', email: v.email },
      )
      aoCriar(c)
    } catch (e) {
      setErro(textoDoErro(e, 'criar'))
    }
  }

  return (
    <form noValidate className="space-y-4" onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <fieldset>
        <legend className="mb-2 text-sm font-semibold">Tipo</legend>
        <div className="flex flex-wrap gap-x-6">
          <Opcao value="discord" {...f.register('kind')}>
            Discord
          </Opcao>
          <Opcao value="email" {...f.register('kind')}>
            E-mail
          </Opcao>
        </div>
      </fieldset>
      {/* key: sem ela, o React reaproveita o mesmo <input> ao trocar o tipo,
          e o texto digitado fica registrado no campo anterior. */}
      {tipo === 'discord' ? (
        <Campo
          key="discord"
          rotulo="URL do webhook"
          erro={erros.url?.message}
          ajuda="Mandamos uma mensagem de teste agora. Webhook de canal de fórum não é aceito."
        >
          {(a) => <Entrada {...a} type="url" {...f.register('url')} />}
        </Campo>
      ) : (
        <Campo key="email" rotulo="E-mail" erro={erros.email?.message} ajuda="Mandamos um link de verificação, válido por 24 horas.">
          {(a) => <Entrada {...a} type="email" {...f.register('email')} />}
        </Campo>
      )}
      <Botao type="submit" disabled={f.formState.isSubmitting}>
        Cadastrar canal
      </Botao>
    </form>
  )
}

export function PaginaCanais() {
  const consulta = useCanais()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const [criando, setCriando] = useState(false)
  const [apagar, setApagar] = useState<Canal | null>(null)
  const [erro, setErro] = useState<string | null>(null)
  // Um POST de reenvio por canal de cada vez (E8 da revisão de 2026-10-14):
  // a ref segura a reentrada no mesmo tique, e o estado desabilita o botão,
  // como no pausar. Um DELETE por vez: sem DELETE em dobro.
  const reenvioEmVoo = useRef(new Set<string>())
  const [reenviando, setReenviando] = useState<ReadonlySet<string>>(new Set())
  const [apagando, setApagando] = useState(false)
  // Pausar pede confirmação (spec do Plano 9 §4.1): os avisos da pausa são
  // descartados. Um PATCH por canal de cada vez (E4 do Plano 10): o de um
  // canal não segura o de outro. A ref segura a reentrada no mesmo tique; o
  // estado alimenta o aria-disabled e o ocupado do Confirmar.
  const [pausar, setPausar] = useState<Canal | null>(null)
  const emVoo = useRef(new Set<string>())
  const [alternando, setAlternando] = useState<ReadonlySet<string>>(new Set())

  const recarregar = () => qc.invalidateQueries({ queryKey: chaves.canais })

  // O canal mudou em outra aba: a lista na tela está velha.
  async function listaVelha(e: unknown): Promise<boolean> {
    if (!(e instanceof ApiError)) return false
    if (e.code === 'channel_not_pending') {
      await recarregar()
      avisar('O canal não está mais aguardando verificação.')
      return true
    }
    if (e.status === 404) {
      await recarregar()
      avisar('O canal já tinha sido apagado, talvez em outra aba.')
      return true
    }
    return false
  }

  async function reenviar(c: Canal) {
    if (reenvioEmVoo.current.has(c.id)) return
    reenvioEmVoo.current.add(c.id)
    setReenviando(new Set(reenvioEmVoo.current))
    setErro(null)
    try {
      await api.reenviarVerificacao(c.id)
      avisar('Link de verificação reenviado.')
    } catch (e) {
      if (!(await listaVelha(e))) setErro(textoDoErro(e, 'reenviar'))
    } finally {
      reenvioEmVoo.current.delete(c.id)
      setReenviando(new Set(reenvioEmVoo.current))
    }
  }

  // Pausa (enabled false) ou retoma (true). O despacho cancela os avisos de
  // canal pausado na hora do envio (semEnvio, Plano 3): retomar não reenvia
  // o que passou.
  async function alternar(c: Canal) {
    if (emVoo.current.has(c.id)) return
    emVoo.current.add(c.id)
    setAlternando(new Set(emVoo.current))
    const enabled = !c.enabled
    setErro(null)
    try {
      await api.pausarCanal(c.id, enabled)
      await recarregar()
      // Com dois canais mudando juntos, o aviso diz qual.
      avisar(`Canal ${nomeDoCanal(c)} ${enabled ? 'retomado' : 'pausado'}.`)
    } catch (e) {
      if (!(await listaVelha(e))) {
        setErro(`Não foi possível ${enabled ? 'retomar' : 'pausar'} o canal ${nomeDoCanal(c)}. ${mensagemDe(e)}`)
      }
    } finally {
      emVoo.current.delete(c.id)
      setAlternando(new Set(emVoo.current))
      // Só o Confirmar DESTE canal fecha: com um Retomar em voo, o Pausar de
      // outro canal pode ter sido aberto no meio (revisão final do Plano 9).
      setPausar((p) => (p?.id === c.id ? null : p))
    }
  }

  async function confirmarApagar() {
    if (!apagar) return
    setErro(null)
    setApagando(true)
    try {
      await api.apagarCanal(apagar.id)
      await recarregar()
      avisar('Canal apagado.')
    } catch (e) {
      if (!(await listaVelha(e))) setErro(mensagemDe(e))
    } finally {
      setApagando(false)
      setApagar(null)
    }
  }

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <Titulo aba="Canais">Canais de notificação</Titulo>
        {/* Vazio é o que o servidor respondeu: carregando ou com erro, o botão
            fica. Com a lista vazia, a ação primária é a do bloco. */}
        {consulta.data?.items.length !== 0 && (
          <BotaoQueSome reserva={TITULO_DA_TELA} onClick={() => setCriando(true)}>
            Novo canal
          </BotaoQueSome>
        )}
      </div>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <EstadoDaConsulta consulta={consulta}>
        {(d) =>
          d.items.length === 0 ? (
            <div className={cn(estiloDeCartao, 'flex flex-col items-center gap-4 text-center')}>
              <span className="inline-flex size-12 items-center justify-center rounded-full bg-primaria-100 text-primaria">
                <Bell aria-hidden="true" className="size-6" />
              </span>
              <p className="text-suave">Nenhum canal. Sem canal verificado, os incidentes não notificam ninguém.</p>
              <BotaoQueSome reserva={TITULO_DA_TELA} onClick={() => setCriando(true)}>
                Novo canal
              </BotaoQueSome>
            </div>
          ) : (
            <ul className="space-y-3">
              {d.items.map((c) => (
                <li key={c.id} className={cn(estiloDeCartao, 'flex flex-wrap items-center justify-between gap-4')}>
                  <div className="min-w-0 space-y-2 break-words">
                    <p className="font-semibold">
                      {rotuloDoTipo(c.kind)} <span className="font-normal text-suave">{nomeDoCanal(c)}</span>
                    </p>
                    <EstadoDoCanal canal={c} />
                    {c.last_error && c.last_failure_at && (
                      <p className="text-sm text-suave">
                        {c.status === 'failing' ? 'Falha' : 'Última falha'}: {textoDoErroDeCanal(c.last_error)}, em{' '}
                        {dataHora(c.last_failure_at)}
                      </p>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {c.kind !== 'desconhecido' && (c.status === 'pending' || c.status === 'expired') && (
                      <Botao
                        tipo="secundario"
                        tamanho="pequeno"
                        disabled={reenviando.has(c.id)}
                        onClick={() => void reenviar(c)}
                        aria-label={`Reenviar verificação do canal ${nomeDoCanal(c)}`}
                      >
                        Reenviar verificação
                      </Botao>
                    )}
                    {/* Um botão só, que muda de texto: o foco devolvido pelo
                        Confirmar ("Pausar") cai no mesmo elemento, agora "Retomar". */}
                    <Botao
                      tipo="secundario"
                      tamanho="pequeno"
                      // aria-disabled, e não disabled: o Retomar não abre
                      // diálogo, e o botão desabilitado perderia o foco para o
                      // <body>. A guarda contra o clique duplo é a de alternar().
                      aria-disabled={alternando.has(c.id) || undefined}
                      onClick={() => {
                        if (emVoo.current.has(c.id)) return
                        if (c.enabled) setPausar(c)
                        else void alternar(c)
                      }}
                      aria-label={`${c.enabled ? 'Pausar' : 'Retomar'} o canal ${nomeDoCanal(c)}`}
                    >
                      {c.enabled ? 'Pausar' : 'Retomar'}
                    </Botao>
                    <Botao
                      tipo="terciario"
                      tamanho="pequeno"
                      onClick={() => setApagar(c)}
                      aria-label={`Apagar o canal ${nomeDoCanal(c)}`}
                    >
                      Apagar
                    </Botao>
                  </div>
                </li>
              ))}
            </ul>
          )
        }
      </EstadoDaConsulta>
      <Dialogo aberto={criando} aoMudar={setCriando} titulo="Novo canal">
        <NovoCanal
          aoCriar={(c) => {
            setCriando(false)
            void recarregar()
            avisar(c.kind === 'email' ? 'Enviamos o link de verificação.' : 'Canal verificado com a mensagem de teste.')
          }}
        />
      </Dialogo>
      <Confirmar
        aberto={pausar !== null}
        aoMudar={(v) => !v && setPausar(null)}
        titulo="Pausar o canal?"
        descricao="Enquanto pausado, os avisos de incidentes deste canal são descartados, e não são reenviados ao retomar."
        acao="Pausar canal"
        ocupado={pausar !== null && alternando.has(pausar.id)}
        aoConfirmar={() => pausar && void alternar(pausar)}
      />
      <Confirmar
        aberto={apagar !== null}
        aoMudar={(v) => !v && setApagar(null)}
        titulo="Apagar o canal?"
        descricao={apagar ? `O canal ${nomeDoCanal(apagar)} deixa de receber os alertas.` : ''}
        acao="Apagar canal"
        ocupado={apagando}
        aoConfirmar={() => void confirmarApagar()}
      />
    </>
  )
}
