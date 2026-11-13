import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada } from '@/components/ui/campo'
import { chaves } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import { pontosDeCodigo } from '@/lib/format'
import { erroDeCredencial, AvisoDeCredencial } from './credencial'
import type { ErroDeCredencial } from './credencial'

// Política de senha nova do Plano 5 (D1): 15 a 128 code points, qualquer
// caractere, sem regra de composição. O servidor é a fonte da verdade.
const novaSenha = z
  .string()
  .refine((s) => pontosDeCodigo(s) >= 15, 'Mínimo de 15 caracteres.')
  .refine((s) => pontosDeCodigo(s) <= 128, 'Máximo de 128 caracteres.')

export function TrocaDeSenha({ comTotp }: { comTotp: boolean }) {
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const esquema = z
    .object({ atual: z.string().min(1, 'Obrigatório.'), nova: novaSenha, confirmacao: z.string(), codigo: z.string() })
    .refine((v) => v.nova === v.confirmacao, { path: ['confirmacao'], message: 'As duas senhas não são iguais.' })
    .refine((v) => !comTotp || v.codigo.trim().length >= 6, { path: ['codigo'], message: 'Informe o código.' })
  const f = useForm({
    resolver: zodResolver(esquema),
    defaultValues: { atual: '', nova: '', confirmacao: '', codigo: '' },
  })
  const [erro, setErro] = useState<ErroDeCredencial | null>(null)
  const e = f.formState.errors

  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={f.handleSubmit(async (v) => {
        setErro(null)
        // comTotp vem do /me em cache: outra aba pode ter ligado o 2FA, e a
        // troca sem código contaria uma prova errada (R34). Relido, o /me
        // mostra o campo do código, e o envio fica para o usuário.
        try {
          const eu = await qc.fetchQuery({ queryKey: chaves.me, queryFn: api.me, staleTime: 0 })
          if (eu.totp_enabled && !comTotp) {
            setErro({ texto: 'A verificação em duas etapas foi ativada. Informe o código.', renovavel: false })
            return
          }
        } catch (err) {
          setErro({ texto: mensagemDe(err), renovavel: false })
          return
        }
        const codigo = comTotp ? v.codigo.replace(/\s+/g, '') : undefined
        try {
          await api.trocarSenha({
            current_password: v.atual,
            new_password: v.nova,
            ...(codigo !== undefined ? { totp_code: codigo } : {}),
          })
          f.reset()
          await qc.invalidateQueries({ queryKey: chaves.sessoes })
          avisar(
            'Senha alterada. As outras sessões foram encerradas e não podem iniciar novos acessos. Ações já em andamento podem ser concluídas.',
          )
        } catch (err) {
          if (err instanceof ApiError && err.fields[0]?.field === 'new_password') {
            f.setError(
              'nova',
              { message: mensagemDoCampo('new_password', err.fields[0].reason) },
              { shouldFocus: true },
            )
          } else {
            // Não há como separar senha errada de código errado, e não se deve.
            setErro(
              erroDeCredencial(err, comTotp ? 'Senha atual ou código incorretos.' : 'Senha atual incorreta.', codigo),
            )
          }
        }
      })}
    >
      <AvisoDeCredencial erro={erro} aoMudar={setErro} />
      <Campo rotulo="Senha atual" erro={e.atual?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...f.register('atual')} />}
      </Campo>
      <Campo rotulo="Senha nova" erro={e.nova?.message} ajuda="De 15 a 128 caracteres. Uma frase serve.">
        {(a) => <Entrada {...a} type="password" autoComplete="new-password" {...f.register('nova')} />}
      </Campo>
      <Campo rotulo="Confirme a senha nova" erro={e.confirmacao?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="new-password" {...f.register('confirmacao')} />}
      </Campo>
      {comTotp && (
        <Campo rotulo="Código do autenticador" erro={e.codigo?.message} ajuda="Ou um código de recuperação.">
          {(a) => <Entrada {...a} autoComplete="one-time-code" {...f.register('codigo')} />}
        </Campo>
      )}
      <Botao type="submit" disabled={f.formState.isSubmitting}>
        Trocar a senha
      </Botao>
    </form>
  )
}
