package tenancy

import (
	"slices"

	"github.com/google/uuid"
)

// Member é a pertença de um usuário a um projeto.
type Member struct {
	UserID uuid.UUID
	Role   Role
}

// ChangeRole devolve uma cópia de members com o papel de userID trocado para
// to. Recusa papel desconhecido, não membro e a troca que deixaria o projeto
// sem owner.
func ChangeRole(members []Member, userID uuid.UUID, to Role) ([]Member, error) {
	if !to.valid() {
		return nil, ErrUnknownRole
	}
	i := indexOf(members, userID)
	if i < 0 {
		return nil, ErrNotMember
	}
	out := slices.Clone(members)
	out[i].Role = to
	if !hasOwner(out) {
		return nil, ErrLastOwner
	}
	return out, nil
}

// RemoveMember devolve uma cópia de members sem userID. Recusa não membro e a
// remoção do último owner.
func RemoveMember(members []Member, userID uuid.UUID) ([]Member, error) {
	i := indexOf(members, userID)
	if i < 0 {
		return nil, ErrNotMember
	}
	out := slices.Delete(slices.Clone(members), i, i+1)
	if !hasOwner(out) {
		return nil, ErrLastOwner
	}
	return out, nil
}

func indexOf(members []Member, userID uuid.UUID) int {
	return slices.IndexFunc(members, func(m Member) bool { return m.UserID == userID })
}

func hasOwner(members []Member) bool {
	return slices.ContainsFunc(members, func(m Member) bool { return m.Role == RoleOwner })
}
