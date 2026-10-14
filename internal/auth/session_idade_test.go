package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/auth"
)

// rotacionarAte gira o refresh a cada 20 dias (dentro do RefreshTTL) até a
// família ter a idade pedida, e devolve o último token e a última sessão.
func rotacionarAte(t *testing.T, mgr *auth.Manager, clk interface{ Advance(time.Duration) }, tok string, idade time.Duration) (string, auth.Session) {
	t.Helper()
	const passo = 20 * 24 * time.Hour
	var s auth.Session
	for andado := time.Duration(0); andado+passo <= idade; andado += passo {
		clk.Advance(passo)
		var err error
		tok, s, err = mgr.Rotate(context.Background(), tok, meta)
		if err != nil {
			t.Fatalf("rotação aos %v: %v", andado+passo, err)
		}
	}
	return tok, s
}

// Rotacionar empurra o vencimento do token, mas não o da família: um refresh
// roubado de um navegador abandonado não vale para sempre. Aos 80 dias de
// rotações a família ainda vive; depois de MaxFamilyAge, nenhuma rotação passa.
func TestFamilyCannotOutliveMaxFamilyAge(t *testing.T) {
	mgr, _, clk, userID := setup(t)
	inicio := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	tok, _, err := mgr.Issue(context.Background(), userID, carimbo, meta)
	if err != nil {
		t.Fatal(err)
	}
	tok, s := rotacionarAte(t, mgr, clk, tok, 80*24*time.Hour)

	// O token emitido aos 80 dias vence com a família, e não 30 dias depois.
	if limite := inicio.Add(auth.MaxFamilyAge); !s.ExpiresAt.Equal(limite) {
		t.Errorf("o token dos 80 dias vence em %v, queria %v (fim da família)", s.ExpiresAt, limite)
	}

	clk.Advance(auth.MaxFamilyAge - 80*24*time.Hour) // exatamente MaxFamilyAge
	if _, _, err := mgr.Rotate(context.Background(), tok, meta); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("rotação com a família em MaxFamilyAge: erro = %v, queria ErrInvalidSession", err)
	}
}

// Um minuto antes de MaxFamilyAge a rotação ainda passa (off point).
func TestFamilyRotatesUntilJustBeforeMaxFamilyAge(t *testing.T) {
	mgr, _, clk, userID := setup(t)

	tok, _, err := mgr.Issue(context.Background(), userID, carimbo, meta)
	if err != nil {
		t.Fatal(err)
	}
	tok, _ = rotacionarAte(t, mgr, clk, tok, 80*24*time.Hour)
	clk.Advance(auth.MaxFamilyAge - 80*24*time.Hour - time.Minute)
	if _, _, err := mgr.Rotate(context.Background(), tok, meta); err != nil {
		t.Errorf("rotação um minuto antes de MaxFamilyAge: %v", err)
	}
}
