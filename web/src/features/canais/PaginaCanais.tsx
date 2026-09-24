import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
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
  expired: { texto: 'Verificação expirada', cor: 'text-sem-dados' },
  failing: { texto: '✕ Falhando', cor: 'text-fora' },
}

const esquema = z.discriminatedUnion('kind', [
  z.object({ kind: z.literal('discord'), url: z.string().trim().pipe(z.url('Cole a URL do webhook.')) }),
  z.object({ kind: z.literal('email'), email: z.string().trim().pipe(z.email('Informe um e-mail válido.')) }),
])
type Valores = z.input<typeof esquema>

function NovoCanal({ aoCriar }: { aoCriar: (c: Canal) => void }) {
  const api = useApi()
  const f = useForm<Valores>({
    resolver: zodResolver(esquema),
    defaultValues: { kind: 'discord', url: '' } as Valores,
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
      if (e instanceof ApiError && e.code === 'validation_failed' && e.fields[0]) {
        setErro(mensagemDoCampo(e.fields[0].field, e.fields[0].reason))
      } else if (e instanceof ApiError && e.code === 'rate_limited') {
        setErro('Limite de envios atingido: tente de novo em alguns minutos.')
      } else {
        setErro(mensagemDe(e))
      }
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

  const recarregar = () => qc.invalidateQueries({ queryKey: ['channels'] })

  async function reenviar(c: Canal) {
    setErro(null)
    try {
      await api.reenviarVerificacao(c.id)
      avisar('Link de verificação reenviado.')
    } catch (e) {
      if (e instanceof ApiError && e.code === 'channel_not_pending') await recarregar()
      else setErro(mensagemDe(e))
    }
  }

  async function confirmarApagar() {
    if (!apagar) return
    try {
      await api.apagarCanal(apagar.id)
      await recarregar()
      avisar('Canal apagado.')
    } catch (e) {
      setErro(mensagemDe(e))
    } finally {
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
                <li key={c.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-borda p-4">
                  <div className="space-y-1">
                    <p className="font-semibold">
                      {c.kind === 'discord' ? 'Discord' : 'E-mail'} <span className="font-normal text-suave">{c.hint}</span>
                    </p>
                    <p className={`text-sm font-medium ${estados[c.status].cor}`}>{estados[c.status].texto}</p>
                    {c.last_error && c.last_failure_at && (
                      <p className="text-sm text-suave">
                        {c.status === 'failing' ? 'Falha' : 'Última falha'}: {textoDoErroDeCanal(c.last_error)}, em{' '}
                        {dataHora(c.last_failure_at)}
                      </p>
                    )}
                  </div>
                  <div className="flex gap-2">
                    {(c.status === 'pending' || c.status === 'expired') && (
                      <Botao tipo="secundario" tamanho="pequeno" onClick={() => void reenviar(c)}>
                        Reenviar verificação
                      </Botao>
                    )}
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
        aberto={apagar !== null}
        aoMudar={(v) => !v && setApagar(null)}
        titulo="Apagar o canal?"
        descricao={`O canal ${apagar?.hint ?? ''} deixa de receber os alertas.`}
        acao="Apagar canal"
        aoConfirmar={() => void confirmarApagar()}
      />
    </>
  )
}
