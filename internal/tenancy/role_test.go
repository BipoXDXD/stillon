package tenancy

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestRolePermissionMatrix(t *testing.T) {
	type linha struct {
		perm                           Permission
		viewer, member, manager, owner bool
	}
	matriz := []struct {
		nome string
		linha
	}{
		{"Read", linha{PermRead, true, true, true, true}},
		{"EditMonitors", linha{PermEditMonitors, false, true, true, true}},
		{"ManageChannels", linha{PermManageChannels, false, false, true, true}},
		{"ManageStatusPage", linha{PermManageStatusPage, false, false, true, true}},
		{"InviteBasic", linha{PermInviteBasic, false, false, true, true}},
		{"ManageMembers", linha{PermManageMembers, false, false, false, true}},
		{"ManageProject", linha{PermManageProject, false, false, false, true}},
	}
	for _, m := range matriz {
		esperado := map[Role]bool{
			RoleViewer:  m.viewer,
			RoleMember:  m.member,
			RoleManager: m.manager,
			RoleOwner:   m.owner,
		}
		for papel, quer := range esperado {
			t.Run(m.nome+"/"+string(papel), func(t *testing.T) {
				if got := papel.Can(m.perm); got != quer {
					t.Fatalf("%s.Can(%s) = %v, quer %v", papel, m.nome, got, quer)
				}
			})
		}
	}
}

func TestUnknownRoleHasNoPermission(t *testing.T) {
	desconhecido := Role("admin")
	perms := []Permission{PermRead, PermEditMonitors, PermManageChannels, PermManageStatusPage,
		PermInviteBasic, PermManageMembers, PermManageProject, Permission(0), Permission(99)}
	for _, p := range perms {
		if desconhecido.Can(p) {
			t.Errorf("Role(admin).Can(%d) = true, quer false", p)
		}
	}
	for _, s := range []string{"admin", "", "Owner", " owner"} {
		t.Run("ParseRole/"+s, func(t *testing.T) {
			if _, err := ParseRole(s); !errors.Is(err, ErrUnknownRole) {
				t.Fatalf("ParseRole(%q) err = %v, quer ErrUnknownRole", s, err)
			}
		})
	}
}

func TestParseRoleKnown(t *testing.T) {
	for _, r := range []Role{RoleOwner, RoleManager, RoleMember, RoleViewer} {
		t.Run(string(r), func(t *testing.T) {
			got, err := ParseRole(string(r))
			if err != nil || got != r {
				t.Fatalf("ParseRole(%q) = %q, %v", r, got, err)
			}
		})
	}
}

func TestPermissionZeroIsDeniedToOwner(t *testing.T) {
	if RoleOwner.Can(Permission(0)) {
		t.Fatal("Permission(0) não deve ser concedida")
	}
}

func TestProjectScopeRequire(t *testing.T) {
	casos := []struct {
		nome  string
		scope ProjectScope
		perm  Permission
		quer  error
	}{
		{"viewer sem EditMonitors", ProjectScope{ProjectID: uuid.New(), UserID: uuid.New(), Role: RoleViewer}, PermEditMonitors, ErrForbidden},
		{"owner com EditMonitors", ProjectScope{ProjectID: uuid.New(), UserID: uuid.New(), Role: RoleOwner}, PermEditMonitors, nil},
		{"viewer lê", ProjectScope{Role: RoleViewer}, PermRead, nil},
		{"papel desconhecido", ProjectScope{Role: Role("admin")}, PermRead, ErrForbidden},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := c.scope.Require(c.perm)
			if c.quer == nil && err != nil {
				t.Fatalf("Require = %v, quer nil", err)
			}
			if c.quer != nil && !errors.Is(err, c.quer) {
				t.Fatalf("Require = %v, quer %v", err, c.quer)
			}
		})
	}
}
