package tenancy_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/tenancy"
)

func TestOnlyTitularProposesToAnOwner(t *testing.T) {
	titular := uuid.New()
	outro := uuid.New()
	ator := func(id uuid.UUID, papel tenancy.Role) tenancy.ProjectScope {
		return tenancy.ProjectScope{ProjectID: uuid.New(), UserID: id, Role: papel}
	}
	alvo := func(id uuid.UUID, papel tenancy.Role) tenancy.MemberView {
		return tenancy.MemberView{UserID: id, Role: papel, IsTitular: id == titular}
	}
	casos := []struct {
		nome   string
		ator   tenancy.ProjectScope
		padrao bool
		alvo   tenancy.MemberView
		quer   error
	}{
		{"titular propõe a um owner", ator(titular, tenancy.RoleOwner), false, alvo(outro, tenancy.RoleOwner), nil},
		{"owner que não é o titular propõe", ator(uuid.New(), tenancy.RoleOwner), false, alvo(outro, tenancy.RoleOwner), tenancy.ErrForbidden},
		{"manager propõe", ator(uuid.New(), tenancy.RoleManager), false, alvo(outro, tenancy.RoleOwner), tenancy.ErrForbidden},
		{"titular propõe a um manager", ator(titular, tenancy.RoleOwner), false, alvo(outro, tenancy.RoleManager), tenancy.ErrTransferTarget},
		{"titular propõe a um member", ator(titular, tenancy.RoleOwner), false, alvo(outro, tenancy.RoleMember), tenancy.ErrTransferTarget},
		{"titular propõe a um viewer", ator(titular, tenancy.RoleOwner), false, alvo(outro, tenancy.RoleViewer), tenancy.ErrTransferTarget},
		{"titular propõe a si mesmo", ator(titular, tenancy.RoleOwner), false, alvo(titular, tenancy.RoleOwner), tenancy.ErrTransferTarget},
		{"titular propõe o Projeto padrão", ator(titular, tenancy.RoleOwner), true, alvo(outro, tenancy.RoleOwner), tenancy.ErrTransferTarget},
		{"não titular propõe o Projeto padrão", ator(uuid.New(), tenancy.RoleOwner), true, alvo(outro, tenancy.RoleOwner), tenancy.ErrForbidden},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := tenancy.AuthorizeTransferProposal(c.ator, titular, c.padrao, c.alvo)
			if !errors.Is(err, c.quer) || (c.quer == nil && err != nil) {
				t.Errorf("AuthorizeTransferProposal = %v, queria %v", err, c.quer)
			}
		})
	}
}

func TestOnlyPendingOwnerAcceptsTransfer(t *testing.T) {
	pendente := uuid.New()
	casos := []struct {
		nome     string
		ator     tenancy.ProjectScope
		pendente uuid.UUID
		quer     error
	}{
		{"o pendente owner aceita", tenancy.ProjectScope{UserID: pendente, Role: tenancy.RoleOwner}, pendente, nil},
		{"outro owner aceita", tenancy.ProjectScope{UserID: uuid.New(), Role: tenancy.RoleOwner}, pendente, tenancy.ErrForbidden},
		{"sem proposta", tenancy.ProjectScope{UserID: pendente, Role: tenancy.RoleOwner}, uuid.Nil, tenancy.ErrForbidden},
		{"o pendente já não é owner", tenancy.ProjectScope{UserID: pendente, Role: tenancy.RoleManager}, pendente, tenancy.ErrForbidden},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := tenancy.AuthorizeTransferAcceptance(c.ator, c.pendente)
			if !errors.Is(err, c.quer) || (c.quer == nil && err != nil) {
				t.Errorf("AuthorizeTransferAcceptance = %v, queria %v", err, c.quer)
			}
		})
	}
}

func TestTitularOrPendingCancelsTransfer(t *testing.T) {
	titular, pendente := uuid.New(), uuid.New()
	casos := []struct {
		nome     string
		ator     uuid.UUID
		pendente uuid.UUID
		quer     error
	}{
		{"o titular cancela", titular, pendente, nil},
		{"o titular cancela sem proposta", titular, uuid.Nil, nil},
		{"o pendente recusa", pendente, pendente, nil},
		{"outro owner cancela", uuid.New(), pendente, tenancy.ErrForbidden},
		{"outro cancela sem proposta", uuid.New(), uuid.Nil, tenancy.ErrForbidden},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			ator := tenancy.ProjectScope{UserID: c.ator, Role: tenancy.RoleOwner}
			err := tenancy.AuthorizeTransferCancel(ator, titular, c.pendente)
			if !errors.Is(err, c.quer) || (c.quer == nil && err != nil) {
				t.Errorf("AuthorizeTransferCancel = %v, queria %v", err, c.quer)
			}
		})
	}
}
