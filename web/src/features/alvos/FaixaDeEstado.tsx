import { useQueryClient } from '@tanstack/react-query'
import { Check, Circle, CircleDashed, Pause, Triangle, X, type LucideIcon } from 'lucide-react'
import { useEffect, useId, useRef, useState } from 'react'
import { useApi } from '@/app/contexto'
import { Selo } from '@/components/SeloDeEstado'
import { Botao } from '@/components/ui/botao'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'
import { dataHora } from '@/lib/format'
import { cn } from '@/lib/utils'
import { faixaDoEstado } from './estado-do-alvo'

// Forma (ícone), cor (fundo 100 e ícone) e texto (título), como o selo. O
// texto da faixa é texto e suave, os pares do src/tokens.test.ts.
const VISUAL: Record<EstadoExibido, { Icone: LucideIcon; fundo: string; cor: string }> = {
  up: { Icone: Circle, fundo: 'bg-no-ar-100', cor: 'text-no-ar' },
  degraded: { Icone: Triangle, fundo: 'bg-degradado-100', cor: 'text-degradado' },
  down: { Icone: X, fundo: 'bg-fora-100', cor: 'text-fora' },
  unknown: { Icone: CircleDashed, fundo: 'bg-sem-dados-100', cor: 'text-sem-dados' },
  pausado: { Icone: Pause, fundo: 'bg-sem-dados-100', cor: 'text-sem-dados' },
}

type Props = {
  item: ItemDoResumo
  incidentes: readonly Incidente[]
  agora: Date
  aoSumir: () => void
  aoErrar: (mensagem: string | null) => void
}

// A faixa de estado do detalhe (spec do redesign, Telas > 2), com o
// Reconhecer quando há incidente aberto sem ack. O Reconhecer é primário: o
// secundário não tem 3:1 sobre os tons 100 do escuro.
export function FaixaDeEstado({ item, incidentes, agora, aoSumir, aoErrar }: Props) {
  const api = useApi()
  const qc = useQueryClient()
  const idDoTitulo = useId()
  const idDaAjuda = useId()
  const [reconhecendo, setReconhecendo] = useState(false)
  const reconhecido = useRef<HTMLParagraphElement>(null)
  // O Reconhecer some quando o ack vale, e o foco iria ao <body>: vai ao
  // "Reconhecido" (o mesmo cuidado da tela de incidentes, I1). Um ref e um
  // efeito a cada render. A lista relida vem do pai, num render que pode
  // chegar depois do de sair do "reconhecendo", com o foco ainda no botão:
  // o pedido espera o "Reconhecido" existir, e cai se o foco foi a outro
  // controle ou se o incidente deixou de estar aberto.
  const aberto = incidentes.find((i) => i.target_id === item.id && i.resolved_at === null) ?? null
  const botao = useRef<HTMLButtonElement>(null)
  const focarDepois = useRef(false)
  useEffect(() => {
    if (!focarDepois.current) return
    const ativo = document.activeElement
    const focoEmOutroControle = ativo !== null && ativo !== document.body && ativo !== botao.current
    if (focoEmOutroControle || aberto === null) {
      focarDepois.current = false
      return
    }
    if (reconhecido.current === null) return
    focarDepois.current = false
    reconhecido.current.focus()
  })

  const faixa = faixaDoEstado(item, aberto, agora)
  const { Icone, fundo, cor } = VISUAL[faixa.estado]

  async function reconhecer(id: string) {
    aoErrar(null)
    setReconhecendo(true)
    try {
      await api.reconhecer(id)
    } catch (e) {
      // 404: o incidente sumiu com o alvo, apagado em outra aba.
      if (naoEncontrado(e)) {
        aoSumir()
        return
      }
      aoErrar(mensagemDe(e))
    } finally {
      // Mesmo depois de uma falha: o ack pode ter chegado ao servidor.
      await qc.invalidateQueries({ queryKey: ['incidents'] })
      focarDepois.current = true
      setReconhecendo(false)
    }
  }

  return (
    <section aria-labelledby={idDoTitulo} className={cn('flex flex-wrap items-start gap-4 rounded-padrao p-4 sm:p-6', fundo)}>
      <span className={cn('inline-flex size-12 shrink-0 items-center justify-center rounded-full bg-superficie', cor)}>
        <Icone aria-hidden="true" className="size-6" strokeWidth={2.5} />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <h2 id={idDoTitulo} className="text-xl sm:text-2xl">
          {faixa.titulo}
        </h2>
        <p className="text-sm text-suave">{faixa.detalhe}</p>
        {aberto?.acknowledged_at && (
          // tabIndex -1: o foco vem para cá quando o Reconhecer some.
          <p ref={reconhecido} tabIndex={-1} className="flex flex-wrap items-center gap-2 pt-1 text-sm text-suave outline-none">
            <Selo tom="primaria" Icone={Check}>
              Reconhecido
            </Selo>
            lembretes parados desde {dataHora(aberto.acknowledged_at)}
          </p>
        )}
      </div>
      {aberto && !aberto.acknowledged_at && (
        <div className="max-w-xs space-y-2">
          <Botao
            ref={botao}
            // aria-disabled, e não disabled: o botão desabilitado perderia o
            // foco para o <body> enquanto o POST voa.
            aria-disabled={reconhecendo || undefined}
            aria-describedby={idDaAjuda}
            onClick={() => {
              if (!reconhecendo) void reconhecer(aberto.id)
            }}
          >
            Reconhecer
          </Botao>
          <p id={idDaAjuda} className="text-meta text-suave">
            Reconhecer interrompe os lembretes; não resolve o incidente.
          </p>
        </div>
      )}
    </section>
  )
}
