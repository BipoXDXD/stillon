package tenancy

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Teto de membros de um projeto, ALÉM do titular, contado pelo titular: o
// operador tem folga para administrar; os demais, uma equipe pequena.
const (
	MaxMembersPerProject  = 3
	MaxMembersForOperator = 100
)

// MemberLimit diz quantos membros, além do titular, o projeto pode ter.
func MemberLimit(titularIsOperator bool) int {
	if titularIsOperator {
		return MaxMembersForOperator
	}
	return MaxMembersPerProject
}

// MemberView é um membro do projeto como a equipe o vê.
type MemberView struct {
	UserID    uuid.UUID
	Email     string
	Role      Role
	IsTitular bool
	JoinedAt  time.Time
}

// AuthorizeInvite diz se actor pode convidar alguém com o papel dado: member
// e viewer pedem PermInviteBasic (manager para cima); manager e owner,
// PermManageMembers (só owner).
func AuthorizeInvite(actor ProjectScope, role Role) error {
	if !role.valid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	if role == RoleMember || role == RoleViewer {
		return actor.Require(PermInviteBasic)
	}
	return actor.Require(PermManageMembers)
}

// AuthorizeRoleChange diz se actor pode dar o papel to a target. Mudar papel
// é de owner; o titular não perde o papel de owner (antes, transfere).
func AuthorizeRoleChange(actor ProjectScope, target MemberView, to Role) error {
	if !to.valid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, to)
	}
	if err := actor.Require(PermManageMembers); err != nil {
		return err
	}
	if target.IsTitular && to != RoleOwner {
		return ErrTitularProtected
	}
	return nil
}

// AuthorizeRemoval diz se actor pode tirar target do projeto. Sair (target é
// o próprio actor) vale para qualquer papel; remover outro é de owner. O
// titular não sai nem é removido.
func AuthorizeRemoval(actor ProjectScope, target MemberView) error {
	if target.UserID == actor.UserID {
		if target.IsTitular {
			return ErrTitularProtected
		}
		return nil
	}
	if err := actor.Require(PermManageMembers); err != nil {
		return err
	}
	if target.IsTitular {
		return ErrTitularProtected
	}
	return nil
}
