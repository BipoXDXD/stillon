import { ShieldCheck, ShieldMinus, ShieldQuestionMark, ShieldX, type LucideIcon } from 'lucide-react'
import type { Alvo } from '@/lib/api/schemas'
import { certificadoDoAlvo, type FormaDoCertificado } from './certificado'

// O ícone repete a forma que o texto já diz: a cor nunca é o único sinal.
const ICONES: Record<FormaDoCertificado, LucideIcon> = {
  valido: ShieldCheck,
  vencido: ShieldX,
  aguardando: ShieldQuestionMark,
  'nao-se-aplica': ShieldMinus,
}

type Props = { alvo: Alvo; agora: Date }

// A linha do certificado abaixo dos indicadores (spec §8, "Alvo: detalhe").
// Só aparece com o alvo já carregado: carregando e erro são da página. O
// relógio é o da consulta do alvo, como o dos indicadores é o do resumo.
// Vencido é o único que pede atenção: texto pleno e peso 600, no lugar de uma
// cor de perigo que acusaria o alvo de estar fora do ar.
export function CertificadoDoAlvo({ alvo, agora }: Props) {
  const certificado = certificadoDoAlvo(alvo, agora)
  const Icone = ICONES[certificado.forma]
  const destaque = certificado.forma === 'vencido' ? 'font-semibold text-texto' : 'text-suave'
  return (
    <p data-testid="certificado" className={`flex items-start gap-2 text-sm ${destaque}`}>
      {/* Uma caixa da altura da linha do text-sm (20 px) centra o ícone na
          primeira linha, também quando o texto quebra no celular, sem margem
          fora da escala. */}
      <span className="flex h-5 shrink-0 items-center">
        <Icone aria-hidden="true" className="size-4" />
      </span>
      <span>{certificado.texto}</span>
    </p>
  )
}
