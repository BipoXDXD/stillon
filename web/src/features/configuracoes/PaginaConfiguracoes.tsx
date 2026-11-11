import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao, BotaoQueSome } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { Dialogo } from '@/components/ui/dialogo'
import { chaves, useMe } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import { dataHora } from '@/lib/format'
import { cn } from '@/lib/utils'
import { TrocaDeSenha } from './TrocaDeSenha'
import { SessoesAtivas } from './SessoesAtivas'
import { JA_ATIVADA, JA_DESATIVADA, NADA_MUDOU, RECUPERACAO_GASTA } from './mensagensDoTotp'
import { AtivarTotp } from './AtivarTotp'
import { DesativarTotp } from './DesativarTotp'
import { TrocarTotp } from './TrocarTotp'

type Dialogo2fa = 'ativar' | 'desativar' | 'trocar'

// E6 do Plano 10: o "Trocar autenticador" clicado com o 2FA já desligado em
// outra aba some no /me relido; BotaoQueSome leva o foco à reserva.
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
  const [avisoDoPortao, setAvisoDoPortao] = useState<string | null>(null)
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
      setAvisoDoPortao(null)
      setDialogo(d)
    }
  }
  const fechar = () => {
    if (incerta) avisar(incerta)
    else if (dialogo === 'trocar' && trocaNoMeio) {
      avisar(
        trocaNoMeio.usouRecuperacao
          ? `${NADA_MUDOU} ${RECUPERACAO_GASTA}`
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
    // O /me fechou a troca: o app atual não pode ser declarado válido com
    // o 2FA desligado. Confirmação incerta continua tendo precedência.
    if (dialogo === 'trocar' && trocaNoMeio) {
      const texto = incerta ?? (totp === false ? JA_DESATIVADA : null)
      if (texto) setAvisoDoPortao(trocaNoMeio.usouRecuperacao ? `${texto} ${RECUPERACAO_GASTA}` : texto)
    }
    setDialogo(null)
    setTrocaNoMeio(null)
    setIncerta(null)
  }
  // O aviso pertence ao provedor de toasts, fora desta renderização.
  useEffect(() => {
    if (avisoDoPortao) avisar(avisoDoPortao)
  }, [avisoDoPortao, avisar])
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
            avisar(incerta ?? JA_ATIVADA)
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
          usouRecuperacao={trocaNoMeio?.usouRecuperacao ?? false}
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
