package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/sentinela/internal/crypto"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	return bytes.Repeat([]byte{7}, 32)
}

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := crypto.NewBox(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	aad := crypto.AAD("targets", uuid.New())
	msg := []byte("Authorization: Bearer abc123")

	sealed, err := box.Seal(msg, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, msg) {
		t.Error("o ciphertext contém o texto em claro")
	}

	opened, err := box.Open(sealed, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened.Reveal(), msg) {
		t.Errorf("Open devolveu %q, queria %q", opened.Reveal(), msg)
	}
}

// O AAD amarra o ciphertext à linha que o guarda. Sem ele, quem tem acesso de
// escrita ao banco pode copiar o segredo de um alvo para outro e fazer o
// sistema usar credenciais de terceiros.
func TestOpenRejectsCiphertextMovedToAnotherRow(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	original := crypto.AAD("targets", uuid.New())
	outra := crypto.AAD("targets", uuid.New())

	sealed, err := box.Seal([]byte("segredo"), original)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := box.Open(sealed, outra); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	aad := crypto.AAD("targets", uuid.New())

	sealed, _ := box.Seal([]byte("segredo"), aad)
	sealed[len(sealed)-1] ^= 0xFF // vira um bit do tag de autenticação

	if _, err := box.Open(sealed, aad); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

func TestOpenRejectsTruncatedInput(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))

	if _, err := box.Open([]byte{1, 2, 3}, nil); !errors.Is(err, crypto.ErrMalformed) {
		t.Errorf("erro = %v, queria ErrMalformed", err)
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	outra, _ := crypto.NewBox(bytes.Repeat([]byte{9}, 32))
	aad := crypto.AAD("targets", uuid.New())

	sealed, _ := box.Seal([]byte("segredo"), aad)

	if _, err := outra.Open(sealed, aad); !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("erro = %v, queria ErrAuthFailed", err)
	}
}

// Nonce repetido em GCM é catastrófico: dois ciphertexts com o mesmo nonce
// permitem recuperar o XOR dos textos em claro. Este teste é uma sentinela
// contra alguém "otimizar" o nonce para um valor fixo.
func TestSealUsesFreshNonceEveryTime(t *testing.T) {
	box, _ := crypto.NewBox(testKey(t))
	aad := crypto.AAD("targets", uuid.New())
	msg := []byte("mesma mensagem")

	vistos := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		sealed, err := box.Seal(msg, aad)
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(sealed[:12])
		if vistos[nonce] {
			t.Fatal("nonce repetido em 100 selagens")
		}
		vistos[nonce] = true
	}
}

func TestNewBoxRejectsWrongKeySize(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := crypto.NewBox(bytes.Repeat([]byte{1}, n)); err == nil {
			t.Errorf("NewBox aceitou chave de %d bytes", n)
		}
	}
}
