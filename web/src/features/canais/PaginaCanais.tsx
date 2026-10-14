import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { Confirmar, Dialogo } from '@/components/ui/dialogo'
import { useCanais } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo, textoDoErroDeCanal } from '@/lib/api/mensagens'
import type { Canal } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'

const estados: Record<Canal['status'], { texto: string; cor: string }> = {
  verified: { texto: '✓ Verificado', cor: 'text-no-ar' },
  pending: { texto: '… Aguardando verificação', cor: 'text-degradado' },
  expired: { texto: '⧖ Verificação expirada', cor: 'text-sem-dados' },
  failing: { texto: '✕ Falhando', cor: 'text-fora' },
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
      <fieldset className="flex gap-4">
        <legend className="mb-2 text-sm font-medium">Tipo</legend>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" value="discord" {...f.register('kind')} /> Discord
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" value="email" {...f.register('kind')} /> E-mail
        </label>
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

  const recarregar = () => qc.invalidateQueries({ queryKey: ['channels'] })

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
      avisar(`Canal ${c.hint} ${enabled ? 'retomado' : 'pausado'}.`)
    } catch (e) {
      if (!(await listaVelha(e))) {
        setErro(`Não foi possível ${enabled ? 'retomar' : 'pausar'} o canal ${c.hint}. ${mensagemDe(e)}`)
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
        <Botao onClick={() => setCriando(true)}>Novo canal</Botao>
      </div>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <EstadoDaConsulta consulta={consulta}>
        {(d) =>
          d.items.length === 0 ? (
            <p className="text-suave">Nenhum canal. Sem canal verificado, os incidentes não notificam ninguém.</p>
          ) : (
            <ul className="space-y-3">
              {d.items.map((c) => (
                <li key={c.id} className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-borda bg-superficie p-4">
                  <div className="min-w-0 space-y-1 break-words">
                    <p className="font-semibold">
                      {c.kind === 'discord' ? 'Discord' : 'E-mail'} <span className="font-normal text-suave">{c.hint}</span>
                    </p>
                    <p className={`text-sm font-medium ${estados[c.status].cor}`}>{estados[c.status].texto}</p>
                    {!c.enabled && <p className="text-sm font-medium">⏸ Pausado: os avisos são descartados</p>}
                    {c.last_error && c.last_failure_at && (
                      <p className="text-sm text-suave">
                        {c.status === 'failing' ? 'Falha' : 'Última falha'}: {textoDoErroDeCanal(c.last_error)}, em{' '}
                        {dataHora(c.last_failure_at)}
                      </p>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {(c.status === 'pending' || c.status === 'expired') && (
                      <Botao
                        tipo="secundario"
                        tamanho="pequeno"
                        disabled={reenviando.has(c.id)}
                        onClick={() => void reenviar(c)}
                        aria-label={`Reenviar verificação do canal ${c.hint}`}
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
                      aria-label={`${c.enabled ? 'Pausar' : 'Retomar'} o canal ${c.hint}`}
                    >
                      {c.enabled ? 'Pausar' : 'Retomar'}
                    </Botao>
                    <Botao
                      tipo="perigo"
                      tamanho="pequeno"
                      onClick={() => setApagar(c)}
                      aria-label={`Apagar o canal ${c.hint}`}
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
        descricao={`O canal ${apagar?.hint ?? ''} deixa de receber os alertas.`}
        acao="Apagar canal"
        ocupado={apagando}
        aoConfirmar={() => void confirmarApagar()}
      />
    </>
  )
}
