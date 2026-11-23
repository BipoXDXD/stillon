package turnstile_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/turnstile"
	"github.com/BipoXDXD/stillon/internal/turnstile/turnstiletest"
)

// TestSiteverifyDeVerdade fala com o siteverify da Cloudflare usando as
// chaves de teste públicas. Pede rede, então só roda com TURNSTILE_REDE=1:
// confere que o servidor falso do turnstiletest imita o de verdade.
func TestSiteverifyDeVerdade(t *testing.T) {
	if os.Getenv("TURNSTILE_REDE") != "1" {
		t.Skip("defina TURNSTILE_REDE=1 para falar com o siteverify de verdade")
	}

	passa := turnstile.New(crypto.Secret(turnstiletest.SecretPassa), "")
	if err := passa.Verificar(context.Background(), turnstiletest.TokenFalso, "", "signup"); err != nil {
		t.Fatalf("secret que passa: Verificar = %v, queria nil", err)
	}

	falha := turnstile.New(crypto.Secret(turnstiletest.SecretFalha), "")
	err := falha.Verificar(context.Background(), turnstiletest.TokenFalso, "", "signup")
	if !errors.Is(err, turnstile.ErrRecusado) {
		t.Fatalf("secret que falha: Verificar = %v, queria ErrRecusado", err)
	}
}
