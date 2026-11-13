import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { Botao, BotaoQueSome } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { useMe } from '@/lib/api/consultas'
import { dataHora } from '@/lib/format'
import { cn } from '@/lib/utils'
import { TrocaDeSenha } from './TrocaDeSenha'
import { SessoesAtivas } from './SessoesAtivas'
import { useDialogosDoTotp } from './useDialogosDoTotp'

// E6 do Plano 10: o "Trocar autenticador" clicado com o 2FA já desligado em
// outra aba some no /me relido; BotaoQueSome leva o foco à reserva.
export function PaginaConfiguracoes() {
  const me = useMe()
  const { abrindo, botao2fa, abrir, dialogos } = useDialogosDoTotp(me.data?.totp_enabled)
  return (
    <>
      <Titulo aba="Configurações">Configurações</Titulo>
      <EstadoDaConsulta consulta={me}>
        {(eu) => (
          <>
            <section aria-labelledby="t-conta" className={cn(estiloDeCartao, 'max-w-2xl space-y-1')}>
              <h2 id="t-conta" className="text-xl">
                Conta
              </h2>
              <p>{eu.email}</p>
              <p className="text-sm text-suave">Desde {dataHora(eu.created_at)}</p>
            </section>
            <section aria-labelledby="t-senha" className={cn(estiloDeCartao, 'max-w-2xl space-y-4')}>
              <h2 id="t-senha" className="text-xl">
                Senha
              </h2>
              <TrocaDeSenha comTotp={eu.totp_enabled} />
            </section>
            <section aria-labelledby="t-2fa" className={cn(estiloDeCartao, 'max-w-2xl space-y-4')}>
              <h2 id="t-2fa" className="text-xl">
                Verificação em duas etapas
              </h2>
              <p className="text-sm">{eu.totp_enabled ? 'Ativada.' : 'Desativada.'}</p>
              <div className="flex flex-wrap gap-2">
                <Botao
                  ref={botao2fa}
                  tipo={eu.totp_enabled ? 'terciario' : 'primario'}
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
      <section aria-labelledby="t-sessoes" className={cn(estiloDeCartao, 'max-w-2xl space-y-4')}>
        <h2 id="t-sessoes" className="text-xl">
          Sessões ativas
        </h2>
        <SessoesAtivas />
      </section>
      {dialogos}
    </>
  )
}
