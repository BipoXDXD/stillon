import { Monitor, Moon, Sun, type LucideIcon } from 'lucide-react'
import { Botao } from '@/components/ui/botao'
import { useTema, type Tema } from '@/lib/tema'

// O tema num botão de ícone (auditoria L5): cada clique passa ao próximo, na
// ordem Sistema → Claro → Escuro → Sistema. O nome acessível diz o tema em
// vigor; o title, o próximo.
const CICLO: Record<Tema, { rotulo: string; Icone: LucideIcon; proximo: Tema }> = {
  sistema: { rotulo: 'Sistema', Icone: Monitor, proximo: 'claro' },
  claro: { rotulo: 'Claro', Icone: Sun, proximo: 'escuro' },
  escuro: { rotulo: 'Escuro', Icone: Moon, proximo: 'sistema' },
}

export function BotaoDeTema() {
  const { tema, mudar } = useTema()
  const { rotulo, Icone, proximo } = CICLO[tema]
  return (
    <Botao
      tipo="terciario"
      tamanho="icone"
      aria-label={`Tema: ${rotulo}`}
      title={`Trocar para ${CICLO[proximo].rotulo}`}
      onClick={() => mudar(proximo)}
    >
      <Icone aria-hidden="true" className="size-5" />
    </Botao>
  )
}
