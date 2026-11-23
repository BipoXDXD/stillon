import { useEffect, useRef, useState } from 'react'
import { Botao } from '@/components/ui/botao'
import { mensagemDe } from '@/lib/api/mensagens'
import { useApi } from './contexto'

// Depois de um reenvio, o botão some por este tempo (proposta da T11, §8). O
// servidor também limita; isto só evita o clique repetido.
const ESPERA_DO_REENVIO_MS = 60_000

type Estado = { fase: 'pronto' } | { fase: 'reenviando' } | { fase: 'reenviado' } | { fase: 'falhou'; mensagem: string }

// A faixa no topo do conteúdo enquanto o e-mail da conta não foi confirmado
// (o Layout a monta com email_verified false). Não se dispensa. <output>, a
// região de status (role implícito): o resultado do reenvio é anunciado sem
// levar o foco a lugar nenhum. Só conteúdo de frase dentro dele (span).
export function AvisoDeConfirmacao({ email }: { email: string }) {
  const api = useApi()
  const [estado, setEstado] = useState<Estado>({ fase: 'pronto' })
  const botao = useRef<HTMLButtonElement>(null)
  const confirmacao = useRef<HTMLSpanElement>(null)
  // O foco estava no botão quando ele deu lugar ao texto: o texto o recebe,
  // e o botão o recebe de volta depois da espera. Sem isso, cairia no <body>.
  const seguirOFoco = useRef(false)

  useEffect(() => {
    if (estado.fase !== 'reenviado') return
    if (seguirOFoco.current) confirmacao.current?.focus()
    const t = setTimeout(() => {
      seguirOFoco.current = document.activeElement === confirmacao.current
      setEstado({ fase: 'pronto' })
    }, ESPERA_DO_REENVIO_MS)
    return () => clearTimeout(t)
  }, [estado.fase])

  useEffect(() => {
    if (estado.fase !== 'pronto' || !seguirOFoco.current) return
    seguirOFoco.current = false
    botao.current?.focus()
  }, [estado.fase])

  async function reenviar() {
    if (estado.fase === 'reenviando') return
    setEstado({ fase: 'reenviando' })
    try {
      await api.reenviarConfirmacao()
      seguirOFoco.current = document.activeElement === botao.current
      setEstado({ fase: 'reenviado' })
    } catch (e) {
      setEstado({ fase: 'falhou', mensagem: mensagemDe(e) })
    }
  }

  return (
    <output
      className="flex flex-col gap-3 rounded-padrao border-l-4 border-primaria bg-primaria-100 p-4 text-sm sm:flex-row sm:items-center sm:justify-between"
    >
      <span className="block space-y-1">
        <span className="block">
          Confirme seu e-mail para criar monitores, canais e páginas. Enviamos um link para{' '}
          <strong className="font-semibold break-all">{email}</strong>.
        </span>
        {estado.fase === 'falhou' && <span className="block font-semibold">{estado.mensagem}</span>}
      </span>
      {estado.fase === 'reenviado' ? (
        <span ref={confirmacao} tabIndex={-1} className="block font-semibold outline-none">
          Link reenviado. Confira sua caixa de entrada.
        </span>
      ) : (
        // aria-disabled, e não disabled, enquanto o pedido voa: o foco não cai no <body>.
        <Botao
          ref={botao}
          tipo="terciario"
          tamanho="pequeno"
          className="self-start sm:self-auto"
          aria-disabled={estado.fase === 'reenviando' || undefined}
          onClick={() => void reenviar()}
        >
          {estado.fase === 'reenviando' ? 'Reenviando…' : 'Reenviar e-mail'}
        </Botao>
      )}
    </output>
  )
}
