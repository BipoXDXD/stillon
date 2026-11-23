import { z } from 'zod'
import { CARACTERE_INVISIVEL } from '@/lib/api/mensagens'
import { pontosDeCodigo } from '@/lib/format'

// tenancy.MaxProjectNameRunes: o servidor conta runas (code points), não
// unidades de UTF-16.
const MAX_NOME = 100

const VAZIO = 'Dê um nome ao projeto.'
const LONGO = `Até ${MAX_NOME} caracteres.`

// O nome do projeto, como tenancy.ParseProjectName: apara as pontas, não
// aceita vazio e conta até 100 runas. O caractere invisível o servidor
// recusa, e o erro dele vem no campo (mensagemDoNome).
export const EsquemaDoProjeto = z.object({
  name: z
    .string()
    .trim()
    .min(1, VAZIO)
    .refine((s) => pontosDeCodigo(s) <= MAX_NOME, LONGO),
})

export type ValoresDoProjeto = z.infer<typeof EsquemaDoProjeto>

// O motivo do 400 do servidor no campo name, com os mesmos textos do
// navegador.
const porMotivo: Record<string, string> = {
  required: VAZIO,
  too_long: LONGO,
  invalid: `${CARACTERE_INVISIVEL}.`,
}

export function mensagemDoNome(motivo: string): string {
  return porMotivo[motivo] ?? 'Valor inválido.'
}
