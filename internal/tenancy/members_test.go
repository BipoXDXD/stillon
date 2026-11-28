package tenancy_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/tenancy"
)

func escopoCom(papel tenancy.Role) tenancy.ProjectScope {
	return tenancy.ProjectScope{ProjectID: uuid.New(), UserID: uuid.New(), Role: papel}
}

func TestAuthorizeInviteMatrix(t *testing.T) {
	casos := []struct {
		nome  string
		ator  tenancy.Role
		papel tenancy.Role
		quer  error
	}{
		{"manager convida member", tenancy.RoleManager, tenancy.RoleMember, nil},
		{"manager convida viewer", tenancy.RoleManager, tenancy.RoleViewer, nil},
		{"manager convida manager", tenancy.RoleManager, tenancy.RoleManager, tenancy.ErrForbidden},
		{"manager convida owner", tenancy.RoleManager, tenancy.RoleOwner, tenancy.ErrForbidden},
		{"owner convida owner", tenancy.RoleOwner, tenancy.RoleOwner, nil},
		{"owner convida manager", tenancy.RoleOwner, tenancy.RoleManager, nil},
		{"owner convida member", tenancy.RoleOwner, tenancy.RoleMember, nil},
		{"owner convida viewer", tenancy.RoleOwner, tenancy.RoleViewer, nil},
		{"member convida viewer", tenancy.RoleMember, tenancy.RoleViewer, tenancy.ErrForbidden},
		{"viewer convida viewer", tenancy.RoleViewer, tenancy.RoleViewer, tenancy.ErrForbidden},
		{"papel desconhecido", tenancy.RoleOwner, tenancy.Role("admin"), tenancy.ErrUnknownRole},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := tenancy.AuthorizeInvite(escopoCom(c.ator), c.papel)
			if !errors.Is(err, c.quer) || (c.quer == nil && err != nil) {
				t.Errorf("AuthorizeInvite = %v, queria %v", err, c.quer)
			}
		})
	}
}

func TestAuthorizeRoleChangeAndRemoval(t *testing.T) {
	alvo := func(papel tenancy.Role, titular bool) tenancy.MemberView {
		return tenancy.MemberView{UserID: uuid.New(), Role: papel, IsTitular: titular}
	}
	owner := escopoCom(tenancy.RoleOwner)
	manager := escopoCom(tenancy.RoleManager)

	mudancas := []struct {
		nome string
		ator tenancy.ProjectScope
		alvo tenancy.MemberView
		para tenancy.Role
		quer error
	}{
		{"owner muda papel de member", owner, alvo(tenancy.RoleMember, false), tenancy.RoleViewer, nil},
		{"manager muda papel", manager, alvo(tenancy.RoleMember, false), tenancy.RoleViewer, tenancy.ErrForbidden},
		{"owner rebaixa o titular", owner, alvo(tenancy.RoleOwner, true), tenancy.RoleManager, tenancy.ErrTitularProtected},
		{"owner rebaixa a si mesmo, não titular", owner,
			tenancy.MemberView{UserID: owner.UserID, Role: tenancy.RoleOwner}, tenancy.RoleMember, nil},
		{"manager tenta rebaixar o titular", manager, alvo(tenancy.RoleOwner, true), tenancy.RoleViewer, tenancy.ErrForbidden},
		{"papel desconhecido", owner, alvo(tenancy.RoleMember, false), tenancy.Role("admin"), tenancy.ErrUnknownRole},
	}
	for _, c := range mudancas {
		t.Run("mudar/"+c.nome, func(t *testing.T) {
			err := tenancy.AuthorizeRoleChange(c.ator, c.alvo, c.para)
			if !errors.Is(err, c.quer) || (c.quer == nil && err != nil) {
				t.Errorf("AuthorizeRoleChange = %v, queria %v", err, c.quer)
			}
		})
	}

	viewer := escopoCom(tenancy.RoleViewer)
	titular := escopoCom(tenancy.RoleOwner)
	remocoes := []struct {
		nome string
		ator tenancy.ProjectScope
		alvo tenancy.MemberView
		quer error
	}{
		{"viewer sai", viewer, tenancy.MemberView{UserID: viewer.UserID, Role: tenancy.RoleViewer}, nil},
		{"titular sai", titular, tenancy.MemberView{UserID: titular.UserID, Role: tenancy.RoleOwner, IsTitular: true},
			tenancy.ErrTitularProtected},
		{"owner remove manager", owner, alvo(tenancy.RoleManager, false), nil},
		{"owner remove o titular", owner, alvo(tenancy.RoleOwner, true), tenancy.ErrTitularProtected},
		{"manager remove member", manager, alvo(tenancy.RoleMember, false), tenancy.ErrForbidden},
		{"viewer remove viewer", viewer, alvo(tenancy.RoleViewer, false), tenancy.ErrForbidden},
	}
	for _, c := range remocoes {
		t.Run("remover/"+c.nome, func(t *testing.T) {
			err := tenancy.AuthorizeRemoval(c.ator, c.alvo)
			if !errors.Is(err, c.quer) || (c.quer == nil && err != nil) {
				t.Errorf("AuthorizeRemoval = %v, queria %v", err, c.quer)
			}
		})
	}
}

func TestMemberLimit(t *testing.T) {
	if n := tenancy.MemberLimit(false); n != 3 {
		t.Errorf("MemberLimit(false) = %d, queria 3 (além do titular)", n)
	}
	if n := tenancy.MemberLimit(true); n != 100 {
		t.Errorf("MemberLimit(true) = %d, queria 100", n)
	}
}
