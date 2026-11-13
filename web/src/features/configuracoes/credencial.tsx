import { useState } from 'react'
import { useSessao } from '@/app/contexto'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import { respostaPerdida } from './respostaPerdida'

// As rotas de credencial (/auth/password, /auth/totp/*) nunca renovam o
// token sozinhas (reconciliação, item 3): um "unauthorized" delas é o
// access token vencido nesta tela (ou, com a Task 11 do Plano 5, a família
// da sessão morta). A tela guarda o formulário e oferece renovar; renovado,
// quem envia de novo é o usuário — nada em /auth/* se repete sozinho.
// renovado: não é erro, é o "Acesso renovado. Envie de novo." — some no
// próximo envio, como qualquer aviso do formulário.
export type ErroDeCredencial = { texto: string; renovavel: boolean; renovado?: boolean }

// Um código que não é de 6 dígitos é de recuperação (16 caracteres base32).
export const ehDeRecuperacao = (codigo: string | undefined) => !!codigo && !/^\d{6}$/.test(codigo)

export function erroDeCredencial(e: unknown, errado: string, codigoEnviado?: string): ErroDeCredencial {
  let r: ErroDeCredencial
  if (e instanceof ApiError && e.code === 'invalid_credentials') r = { texto: errado, renovavel: false }
  else if (e instanceof ApiError && e.code === 'unauthorized') {
    r = { texto: 'O acesso desta tela venceu.', renovavel: true }
  } else r = { texto: mensagemDe(e), renovavel: false }
  // pendência do Plano 6: o código de recuperação é consumido ANTES da
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
export function AvisoDeCredencial({
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
