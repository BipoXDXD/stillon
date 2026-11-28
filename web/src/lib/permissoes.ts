import { useProjeto } from '@/app/projeto'
import type { Papel } from '@/lib/api/schemas'
import matriz from './permissoes.json'

// As permissões de cada papel, com os nomes das constantes de
// internal/tenancy/role.go em snake_case. O permissoes.json é a mesma matriz
// de Role.Can, e o teste Go (internal/tenancy/matriz_painel_test.go) confere.
// O painel só esconde o que o papel não pode; quem decide é o servidor.
export type Permissao =
  | 'read'
  | 'edit_monitors'
  | 'manage_channels'
  | 'manage_status_page'
  | 'invite_basic'
  | 'manage_members'
  | 'manage_project'

export type PapelConhecido = 'owner' | 'manager' | 'member' | 'viewer'

const MATRIZ: Record<PapelConhecido, readonly Permissao[]> = matriz as Record<PapelConhecido, Permissao[]>

// Um papel que o painel não conhece ('desconhecido') só lê: o projeto está na
// lista da pessoa, então ela é membro; o resto falha fechado.
export function pode(papel: Papel, p: Permissao): boolean {
  if (papel === 'desconhecido') return p === 'read'
  return MATRIZ[papel].includes(p)
}

// Como tenancy.AuthorizeInvite: convidar como member ou viewer pede
// invite_basic; como manager ou owner, manage_members.
export function podeConvidarComo(papel: Papel, alvo: PapelConhecido): boolean {
  const exige: Permissao = alvo === 'member' || alvo === 'viewer' ? 'invite_basic' : 'manage_members'
  return pode(papel, exige)
}

// O papel da pessoa no projeto atual concede a permissão?
export function usePode(p: Permissao): boolean {
  return pode(useProjeto().papel, p)
}
