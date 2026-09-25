package crypto_test

import (
	"regexp"
	"testing"

	"github.com/BipoXDXD/stillon/internal/crypto"
)

// O nonce vai para um atributo HTML e para o cabeçalho da CSP, e os dois
// têm de ser o mesmo texto. Um caractere que o html/template escapa ("+"
// vira "&#43;") quebraria isso só às vezes, conforme o sorteio: por isso o
// teste sorteia mil (D26 do Plano 4).
func TestCSPNonceNeedsNoEscaping(t *testing.T) {
	seguro := regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)
	for range 1000 {
		n, err := crypto.NewCSPNonce()
		if err != nil || !seguro.MatchString(n) {
			t.Fatalf("nonce %q (%v): queria 22 caracteres de base64 de URL", n, err)
		}
	}
}

// Um nonce repetido deixa de ser segredo da resposta: quem viu uma página
// saberia o nonce da próxima.
func TestCSPNonceIsFreshEveryTime(t *testing.T) {
	vistos := map[string]bool{}
	for range 1000 {
		n, err := crypto.NewCSPNonce()
		if err != nil {
			t.Fatal(err)
		}
		if vistos[n] {
			t.Fatalf("nonce repetido: %q", n)
		}
		vistos[n] = true
	}
}
