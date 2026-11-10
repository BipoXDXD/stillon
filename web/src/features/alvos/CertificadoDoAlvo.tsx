import type { UseQueryResult } from '@tanstack/react-query'
import { ShieldAlert, ShieldCheck, ShieldMinus, ShieldQuestionMark, ShieldX, type LucideIcon } from 'lucide-react'
import type { Alvo } from '@/lib/api/schemas'
import { CERTIFICADO_INDISPONIVEL, certificadoDoAlvo, type Certificado, type FormaDoCertificado } from './certificado'

// O ícone repete a forma que o texto já diz: a cor nunca é o único sinal.
const ICONES: Record<FormaDoCertificado, LucideIcon> = {
  valido: ShieldCheck,
  vencido: ShieldX,
  aguardando: ShieldQuestionMark,
  'nao-se-aplica': ShieldMinus,
  indisponivel: ShieldAlert,
}

type Props = { consulta: Pick<UseQueryResult<Alvo>, 'data' | 'isLoading' | 'dataUpdatedAt'> }

// A linha do certificado abaixo dos indicadores (spec §8, "Alvo: detalhe").
// O relógio é o da consulta do alvo, como o dos indicadores é o do resumo.
// Sem dados e sem busca, a consulta falhou: "indisponível", no tom neutro,
// porque o que falhou foi a leitura, e não o alvo.
export function CertificadoDoAlvo({ consulta }: Props) {
  if (consulta.data !== undefined) {
    return <LinhaDoCertificado certificado={certificadoDoAlvo(consulta.data, new Date(consulta.dataUpdatedAt))} />
  }
  if (consulta.isLoading) {
    return (
      <div aria-busy="true">
        <span className="sr-only">Carregando o certificado…</span>
        <div
          aria-hidden="true"
          data-testid="esqueleto-do-certificado"
          className="h-5 w-72 max-w-full animate-pulse rounded-padrao bg-painel motion-reduce:animate-none"
        />
      </div>
    )
  }
  return <LinhaDoCertificado certificado={CERTIFICADO_INDISPONIVEL} />
}

// Vencido é o único que pede atenção: texto pleno e peso 600, no lugar de
// uma cor de perigo que acusaria o alvo de estar fora do ar.
function LinhaDoCertificado({ certificado }: { certificado: Certificado }) {
  const Icone = ICONES[certificado.forma]
  const destaque = certificado.forma === 'vencido' ? 'font-semibold text-texto' : 'text-suave'
  return (
    <p data-testid="certificado" className={`flex items-start gap-2 text-sm ${destaque}`}>
      <Icone aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
      <span>{certificado.texto}</span>
    </p>
  )
}
