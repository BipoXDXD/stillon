package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"

	"github.com/BipoXDXD/stillon/internal/auth"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
)

// cadastrarPendente faz o Enroll e devolve o código do segredo pendente em em.
func cadastrarPendente(t *testing.T, svc *auth.TOTP, userID uuid.UUID, em time.Time) string {
	t.Helper()
	uri, err := svc.Enroll(context.Background(), userID, "totp@exemplo.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secretFromURI(t, uri), em)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func familiaEstaViva(t *testing.T, familia uuid.UUID) bool {
	t.Helper()
	var v bool
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM sessions WHERE family_id = $1 AND revoked_at IS NULL AND used_at IS NULL)`,
		familia).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Ligar o 2FA encerra as OUTRAS famílias de sessão e mantém a atual, como a
// troca do segundo fator e a de senha: quem liga o 2FA depois de suspeitar de
// um acesso indevido espera que a sessão do intruso caia junto.
func TestConfirmRevokesTheOtherFamilies(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	em := time.Now()
	atual := familiaViva(t, pool, userID, em)
	outra := familiaViva(t, pool, userID, em)
	code := cadastrarPendente(t, svc, userID, em)

	if _, err := svc.Confirm(context.Background(), userID, atual, code, em); err != nil {
		t.Fatal(err)
	}
	if !familiaEstaViva(t, atual) {
		t.Error("ligar o 2FA encerrou a família atual")
	}
	if familiaEstaViva(t, outra) {
		t.Error("ligar o 2FA deixou viva a outra família")
	}
}

// A família do token pode cair entre a guarda da rota e a transação. Então
// nada muda: o 200 sempre quer dizer "ligado, e a sessão atual continua viva".
func TestConfirmRechecksTheFamily(t *testing.T) {
	svc, pool, userID := setupTOTP(t)
	ctx := context.Background()
	em := time.Now()
	familia := familiaViva(t, pool, userID, em)
	code := cadastrarPendente(t, svc, userID, em)
	if _, err := testdb.Admin(t).Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE family_id = $1`,
		familia, em); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Confirm(ctx, userID, familia, code, em); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("confirmar com a família morta devolveu %v, queria ErrInvalidSession", err)
	}
	ligado, err := svc.IsEnabled(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if ligado {
		t.Error("a confirmação recusada ligou o 2FA")
	}
	if n := contarCodigos(t, pool, userID); n != 0 {
		t.Errorf("a confirmação recusada gravou %d códigos de recuperação", n)
	}

	// A perna positiva: com a família de volta à vida, o mesmo código confirma
	// (o timestep não foi consumido pela tentativa recusada).
	if _, err := testdb.Admin(t).Exec(ctx, `UPDATE sessions SET revoked_at = NULL WHERE family_id = $1`, familia); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Confirm(ctx, userID, familia, code, em); err != nil {
		t.Errorf("confirmar com a família viva devolveu %v", err)
	}
}
