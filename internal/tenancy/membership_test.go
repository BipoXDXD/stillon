package tenancy

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func contaOwners(ms []Member) int {
	n := 0
	for _, m := range ms {
		if m.Role == RoleOwner {
			n++
		}
	}
	return n
}

func TestLastOwnerCannotBeDemotedOrRemoved(t *testing.T) {
	a, b := uuid.New(), uuid.New()

	t.Run("rebaixar o único owner", func(t *testing.T) {
		in := []Member{{a, RoleOwner}, {b, RoleMember}}
		if _, err := ChangeRole(in, a, RoleManager); !errors.Is(err, ErrLastOwner) {
			t.Fatalf("err = %v, quer ErrLastOwner", err)
		}
	})
	t.Run("remover o único owner", func(t *testing.T) {
		if _, err := RemoveMember([]Member{{a, RoleOwner}}, a); !errors.Is(err, ErrLastOwner) {
			t.Fatalf("err = %v, quer ErrLastOwner", err)
		}
	})
	t.Run("com dois owners, rebaixar um passa", func(t *testing.T) {
		in := []Member{{a, RoleOwner}, {b, RoleOwner}}
		out, err := ChangeRole(in, a, RoleViewer)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if n := contaOwners(out); n != 1 {
			t.Fatalf("owners = %d, quer 1", n)
		}
		if out[0].Role != RoleViewer {
			t.Fatalf("A ficou %q, quer viewer", out[0].Role)
		}
		if in[0].Role != RoleOwner {
			t.Fatal("ChangeRole alterou a entrada")
		}
	})
	t.Run("com dois owners, remover um passa", func(t *testing.T) {
		in := []Member{{a, RoleOwner}, {b, RoleOwner}}
		out, err := RemoveMember(in, b)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(out) != 1 || out[0].UserID != a {
			t.Fatalf("out = %v", out)
		}
		if len(in) != 2 || in[1].UserID != b {
			t.Fatal("RemoveMember alterou a entrada")
		}
	})
	t.Run("remover não owner passa", func(t *testing.T) {
		out, err := RemoveMember([]Member{{a, RoleOwner}, {b, RoleMember}}, b)
		if err != nil || len(out) != 1 || contaOwners(out) != 1 {
			t.Fatalf("out = %v, err = %v", out, err)
		}
	})
	t.Run("promover a owner passa", func(t *testing.T) {
		out, err := ChangeRole([]Member{{a, RoleOwner}, {b, RoleMember}}, b, RoleOwner)
		if err != nil || contaOwners(out) != 2 {
			t.Fatalf("out = %v, err = %v", out, err)
		}
	})
	t.Run("papel desconhecido é recusado", func(t *testing.T) {
		in := []Member{{a, RoleOwner}, {b, RoleMember}}
		if _, err := ChangeRole(in, b, Role("admin")); !errors.Is(err, ErrUnknownRole) {
			t.Fatalf("err = %v, quer ErrUnknownRole", err)
		}
	})
}

func TestChangeRoleOfNonMember(t *testing.T) {
	a, estranho := uuid.New(), uuid.New()
	in := []Member{{a, RoleOwner}}
	if _, err := ChangeRole(in, estranho, RoleViewer); !errors.Is(err, ErrNotMember) {
		t.Fatalf("ChangeRole err = %v, quer ErrNotMember", err)
	}
	if _, err := RemoveMember(in, estranho); !errors.Is(err, ErrNotMember) {
		t.Fatalf("RemoveMember err = %v, quer ErrNotMember", err)
	}
}
