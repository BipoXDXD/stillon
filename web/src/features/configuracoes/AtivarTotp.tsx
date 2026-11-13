import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useApi } from '@/app/contexto'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { erroDeCredencial, respostaPerdida, AvisoDeCredencial } from './credencial'
import type { ErroDeCredencial } from './credencial'
import { JA_ATIVADA } from './mensagensDoTotp'
import { esquemaSenha, useEnvioUnico, PassoDoQr, CodigosDeRecuperacao } from './passosDoTotp'

// O confirm responde o mesmo 400 para código errado e para cadastro que
// sumiu (trocar a senha apaga o cadastro pendente: pendência do Plano 6).
const CODIGO_NAO_ACEITO =
  'Código incorreto. Se a senha foi trocada depois de o QR code aparecer, feche e comece de novo.'

// 403 no enroll ou no confirm: o 2FA já está ligado (outra aba o ligou com
// este diálogo aberto; totphandlers.go).
const jaAtivada = (e: unknown) => e instanceof ApiError && e.status === 403

const ATIVACAO_INCERTA =
  'A resposta do servidor não chegou, e não dá para saber se a verificação em duas etapas foi ativada. Feche e confira nesta tela: se ela estiver ativada, os códigos de recuperação desta ativação se perderam; use "Trocar autenticador" para gerar outros.'

export function AtivarTotp({
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
