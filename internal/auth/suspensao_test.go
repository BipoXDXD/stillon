package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// A suspensão é relida sob a trava do usuário, na emissão e na rotação: um
// login que conferiu a senha antes de a suspensão comitar não abre sessão, e
// um refresh emitido antes dela não renova, mesmo que a revogação das
// famílias não tenha acontecido (aqui, a suspensão é gravada por fora).
func TestSuspendedUserGetsNoSession(t *testing.T) {
	mgr, _, _, userID := setup(t)
	ctx := context.Background()
	tok, _, err := mgr.Issue(ctx, userID, carimbo, meta)
	if err != nil {
		t.Fatal(err)
	}

	testdb.Suspender(t, userID)

	if _, _, err := mgr.Issue(ctx, userID, carimbo, meta); !errors.Is(err, auth.ErrSuspended) {
		t.Errorf("Issue de usuário suspenso: %v, queria ErrSuspended", err)
	}
	if _, _, err := mgr.Rotate(ctx, tok, meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("Rotate de usuário suspenso: %v, queria ErrInvalidSession", err)
	}
}
