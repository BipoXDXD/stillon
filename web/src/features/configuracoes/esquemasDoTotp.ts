import { z } from 'zod'

// Um envio vazio iria ao servidor e contaria como tentativa falha no
// bloqueio progressivo (R34). Só "não vazio": o formato do código (6
// dígitos ou os 16 de recuperação) quem decide é o servidor.
export const esquemaSenha = z.object({ senha: z.string().min(1, 'Informe a senha.') })
export const esquemaCodigo = z.object({ codigo: z.string().trim().min(1, 'Informe o código.') })
