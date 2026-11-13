import { useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useApi } from '@/app/contexto'
import { chaves } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { ehDeRecuperacao, erroDeCredencial, type ErroDeCredencial } from './credencial'
import { RECUPERACAO_GASTA } from './mensagensDoTotp'
import { PassoDoQr, CodigosDeRecuperacao, FormularioDeProvas } from './passosDoTotp'
import { respostaPerdida } from './respostaPerdida'

// 403 no rotate/confirm: não há troca pendente. Outra aba concluiu a troca,
// ou a cancelou (desativar apaga o pendente; outro rotate o substitui).
const TROCA_SUMIU = 'A troca foi concluída ou cancelada em outra aba. Comece de novo.'
const TROCA_INCERTA =
  'A resposta do servidor não chegou, e não dá para saber se a troca foi concluída. Saia e entre com um código do app novo: se ele valer, a troca foi feita e os códigos de recuperação antigos não valem mais (troque o autenticador de novo para gerar outros); se não valer, o app atual continua valendo.'

// A troca de autenticador (spec do Plano 9 §4.2), em três passos: as provas
// do Desativar; o QR do app NOVO e o código dele; os 10 códigos novos. O
// app atual vale até o passo 2 ser confirmado.
export function TrocarTotp({
  usouRecuperacao,
  aoIniciar,
  aoVoltar,
  aoOcupar,
  aoTravar,
  aoTerminar,
  aoDesativada,
  aoIncerta,
}: {
  // A prova do rotate aceito já foi consumida, mesmo se o confirm falhar.
  usouRecuperacao: boolean
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
              setErro({
                texto: usouRecuperacao ? `${TROCA_SUMIU} ${RECUPERACAO_GASTA}` : TROCA_SUMIU,
                renovavel: false,
              })
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
