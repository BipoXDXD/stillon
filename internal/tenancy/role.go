// Package tenancy é o contexto de domínio dos projetos: papéis, permissões,
// membros e a regra de que o titular é sempre owner (o que garante pelo menos
// um owner por projeto). É puro: não conhece banco, HTTP nem storage.
package tenancy

import (
	"errors"
	"fmt"
)

// Role é o papel de um usuário num projeto.
type Role string

const (
	RoleOwner   Role = "owner"
	RoleManager Role = "manager"
	RoleMember  Role = "member"
	RoleViewer  Role = "viewer"
)

// DefaultProjectName é o nome do projeto que todo usuário recebe ao ser criado.
const DefaultProjectName = "Projeto padrão"

var (
	ErrUnknownRole     = errors.New("papel desconhecido")
	ErrForbidden       = errors.New("operação não permitida para o papel")
	ErrProjectNotFound = errors.New("projeto não encontrado")
	ErrNotMember       = errors.New("não é membro do projeto")
	// ErrTitularProtected: o titular não é rebaixado, não é removido e não
	// sai; antes, transfere a titularidade.
	ErrTitularProtected = errors.New("o titular do projeto não sai nem perde o papel de owner")
	ErrMemberLimit      = errors.New("limite de membros do projeto atingido")
	ErrAlreadyMember    = errors.New("já é membro do projeto")
)

// Permission é uma ação sobre o projeto que depende do papel.
type Permission uint8

const (
	PermRead             Permission = iota + 1 // ler tudo do projeto
	PermEditMonitors                           // criar/editar/pausar alvos e heartbeats, reconhecer incidentes
	PermManageChannels                         // canais de aviso
	PermManageStatusPage                       // status page
	PermInviteBasic                            // convidar member e viewer
	PermManageMembers                          // membros, papéis, convites de manager/owner
	PermManageProject                          // renomear e excluir
)

// permissoes é a matriz fixada: cada papel herda as do anterior.
var permissoes = map[Role]map[Permission]bool{
	RoleViewer: conjunto(PermRead),
	RoleMember: conjunto(PermRead, PermEditMonitors),
	RoleManager: conjunto(PermRead, PermEditMonitors,
		PermManageChannels, PermManageStatusPage, PermInviteBasic),
	RoleOwner: conjunto(PermRead, PermEditMonitors,
		PermManageChannels, PermManageStatusPage, PermInviteBasic,
		PermManageMembers, PermManageProject),
}

func conjunto(ps ...Permission) map[Permission]bool {
	m := make(map[Permission]bool, len(ps))
	for _, p := range ps {
		m[p] = true
	}
	return m
}

// ParseRole converte o texto gravado no banco ou recebido da API num Role.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if !r.valid() {
		return "", fmt.Errorf("%w: %q", ErrUnknownRole, s)
	}
	return r, nil
}

func (r Role) valid() bool {
	_, ok := permissoes[r]
	return ok
}

// Can diz se o papel concede a permissão. Papel desconhecido não pode nada.
func (r Role) Can(p Permission) bool {
	return permissoes[r][p]
}
