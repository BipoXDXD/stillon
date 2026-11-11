import { Check, Circle, CircleDashed, Pause, Triangle, X, type LucideIcon } from 'lucide-react'
import { useEffect, useId, useRef } from 'react'
import { useApi } from '@/app/contexto'
import { Selo } from '@/components/SeloDeEstado'
import { Botao } from '@/components/ui/botao'
import { useReconhecer } from '@/features/incidentes/reconhecer'
import { chaves } from '@/lib/api/consultas'
import type { Incidente, ItemDoResumo } from '@/lib/api/schemas'
import type { EstadoExibido } from '@/lib/estado'
import { dataHora } from '@/lib/format'
import { cn } from '@/lib/utils'
import { faixaDoEstado } from './estado-do-alvo'

// Forma (ícone), cor (fundo 100 e ícone) e texto (título), como o selo. O
// título é texto; o texto secundário é o 700 do mesmo matiz do fundo, e não
// o cinza suave (RUI, texto sobre fundo colorido). src/tokens.test.ts confere
// os pares desta tabela.
export const VISUAL_DA_FAIXA: Record<EstadoExibido, { Icone: LucideIcon; fundo: string; cor: string; secundario: string }> = {
  up: { Icone: Circle, fundo: 'bg-no-ar-100', cor: 'text-no-ar', secundario: 'text-no-ar-700' },
  degraded: { Icone: Triangle, fundo: 'bg-degradado-100', cor: 'text-degradado', secundario: 'text-degradado-700' },
  down: { Icone: X, fundo: 'bg-fora-100', cor: 'text-fora', secundario: 'text-fora-700' },
  unknown: { Icone: CircleDashed, fundo: 'bg-sem-dados-100', cor: 'text-sem-dados', secundario: 'text-sem-dados-700' },
  pausado: { Icone: Pause, fundo: 'bg-sem-dados-100', cor: 'text-sem-dados', secundario: 'text-sem-dados-700' },
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
  const idDoTitulo = useId()
  const idDaAjuda = useId()
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
  const { Icone, fundo, cor, secundario } = VISUAL_DA_FAIXA[faixa.estado]

  const { reconhecendo, reconhecer } = useReconhecer({
    enviar: api.reconhecer,
    consulta: chaves.todosOsIncidentes,
    aoNaoEncontrar: aoSumir,
    aoErrar,
    aoTerminar: () => {
      focarDepois.current = true
    },
  })

  return (
    <section aria-labelledby={idDoTitulo} className={cn('flex flex-wrap items-start gap-4 rounded-padrao p-4 sm:p-6', fundo)}>
      <span className={cn('inline-flex size-12 shrink-0 items-center justify-center rounded-full bg-superficie', cor)}>
        <Icone aria-hidden="true" className="size-6" strokeWidth={2.5} />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <h2 id={idDoTitulo} className="text-xl sm:text-2xl">
          {faixa.titulo}
        </h2>
        <p className={cn('text-sm', secundario)}>{faixa.detalhe}</p>
        {aberto?.acknowledged_at && (
          // tabIndex -1: o foco vem para cá quando o Reconhecer some.
          <p ref={reconhecido} tabIndex={-1} className={cn('flex flex-wrap items-center gap-2 pt-1 text-sm outline-none', secundario)}>
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
            aria-disabled={reconhecendo.has(aberto.id) || undefined}
            aria-describedby={idDaAjuda}
            onClick={() => void reconhecer(aberto.id)}
          >
            Reconhecer
          </Botao>
          <p id={idDaAjuda} className={cn('text-meta', secundario)}>
            Reconhecer interrompe os lembretes; não resolve o incidente.
          </p>
        </div>
      )}
    </section>
  )
}
