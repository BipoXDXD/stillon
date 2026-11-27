//go:build rede

package turnstile_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
	"github.com/BipoXDXD/stillon/internal/turnstile"
	"github.com/BipoXDXD/stillon/internal/turnstile/turnstiletest"
)

// TestSiteverifyDeVerdade fala com o siteverify da Cloudflare usando as
// chaves de teste públicas e confere que o servidor falso do turnstiletest
// imita o de verdade. Pede rede, então fica fora do `make test` e do CI pela
// build tag:
//
//	go test -tags rede -run TestSiteverifyDeVerdade -v ./internal/turnstile
func TestSiteverifyDeVerdade(t *testing.T) {
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
